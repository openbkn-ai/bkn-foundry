import asyncio
import os
import signal
import multiprocessing
import subprocess
import sys
import time
import atexit

import uvicorn
from app.utils.app_utils import create_app
from app.core.config import base_config
from app.logs.stand_log import StandLogger
from app.utils.config_cache import quota_config_cache_tree  # Initialize the large-model configuration cache.

app = create_app()

# Global reference to the Kafka consumer process.
kafka_consumer_process = None


# Manage the Kafka consumer through FastAPI startup and shutdown events so it also starts under uvicorn main:app.
#
# Key constraint: FastAPI lifespan startup is a synchronous phase. Uvicorn binds the port and
# serves requests, including /api/v1/health/ready, only after all on_event("startup") handlers
# complete in sequence. start_kafka_consumer_process() contains a blocking time.sleep(2) and
# subprocess.Popen, and a slow Kafka broker handshake can delay it further. Awaiting it directly
# in the startup handler delays port binding until the subprocess stabilizes, so the startup probe
# can already receive connection refused and trigger CrashLoopBackOff.
#
# Use fire-and-forget instead: the startup handler schedules a background task and returns
# immediately, allowing uvicorn to finish the lifespan phase, bind the port, and serve /health/ready.
# A Kafka consumer startup failure does not block business APIs, matching the existing fallback
# semantics in _check_and_create_topic.
@app.on_event("startup")
async def _startup_kafka_consumer():
    asyncio.get_event_loop().run_in_executor(None, _start_kafka_consumer_safely)


def _start_kafka_consumer_safely():
    try:
        global kafka_consumer_process
        if kafka_consumer_process is None or kafka_consumer_process.poll() is not None:
            StandLogger.info_log("后台任务：准备启动 Kafka 消费者进程")
            ok = start_kafka_consumer_process()
            if ok:
                StandLogger.info_log("后台任务：Kafka 消费者进程启动成功")
            else:
                StandLogger.warn("后台任务：Kafka 消费者进程启动失败（业务接口不受影响）")
        else:
            StandLogger.info_log("后台任务：检测到 Kafka 消费者进程已在运行，跳过启动")
    except Exception as e:
        StandLogger.error(f"后台任务：启动 Kafka 消费者进程异常: {e}")


@app.on_event("shutdown")
async def _shutdown_kafka_consumer():
    try:
        StandLogger.info_log("FastAPI 停止事件：开始清理 Kafka 消费者进程")
        cleanup_processes()
    except Exception as e:
        StandLogger.error(f"FastAPI 停止事件：清理 Kafka 消费者进程异常: {e}")


def signal_handler(signum, frame):
    """Handle signals by shutting down all processes gracefully."""
    StandLogger.info_log(f"收到信号 {signum}，开始关闭所有进程...")
    
    # Shut down the Kafka consumer process.
    if kafka_consumer_process and kafka_consumer_process.poll() is None:
        StandLogger.info_log("正在关闭 Kafka 消费者进程...")
        kafka_consumer_process.terminate()
        try:
            kafka_consumer_process.wait(timeout=10)  # Wait for up to 10 seconds.
        except subprocess.TimeoutExpired:
            StandLogger.warn("Kafka 消费者进程未能在10秒内关闭，强制终止")
            kafka_consumer_process.kill()
    
    StandLogger.info_log("所有进程已关闭")
    sys.exit(0)


def cleanup_processes():
    """Clean up subprocesses when the application exits."""
    if kafka_consumer_process and kafka_consumer_process.poll() is None:
        StandLogger.info_log("清理 Kafka 消费者进程...")
        kafka_consumer_process.terminate()


def start_kafka_consumer_process():
    """Start the Kafka consumer subprocess."""
    global kafka_consumer_process
    
    try:
        print("正在启动 Kafka 消费者子进程...")  # Console output.
        StandLogger.info_log("启动 Kafka 消费者子进程...")
        
        # Build the command.
        script_path = os.path.join(os.path.dirname(__file__), 'kafka_consumer_process.py')
        print(f"脚本路径: {script_path}")  # Console output.
        print(f"Python 解释器: {sys.executable}")  # Console output.
        
        # Start an independent Kafka consumer process with subprocess.
        # Do not use PIPE so subprocess output is written directly to the console.
        kafka_consumer_process = subprocess.Popen([
            sys.executable, 
            script_path
        ], 
        cwd=os.path.dirname(__file__))
        
        print(f"Kafka 消费者进程已启动，PID: {kafka_consumer_process.pid}")  # Console output.
        StandLogger.info_log(f"Kafka 消费者进程已启动，PID: {kafka_consumer_process.pid}")
        
        # Check whether the process started successfully.
        print("等待进程启动...")  # Console output.
        time.sleep(2)  # Allow two seconds for startup.
        
        if kafka_consumer_process.poll() is not None:
            # The process has already exited.
            print("进程已退出，可能启动失败")  # Console output.
            StandLogger.error("Kafka 消费者进程启动失败，进程已退出")
            return False
        else:
            print("进程仍在运行")  # Console output.
            
        return True
        
    except Exception as e:
        print(f"启动 Kafka 消费者进程时出错: {e}")  # Console output.
        StandLogger.error(f"启动 Kafka 消费者进程时出错: {e}")
        import traceback
        traceback.print_exc()  # Print detailed error information.
        return False


if __name__ == '__main__':
    print(f"NH_DEBUG={os.getenv('NH_DEBUG')}")
    if os.getenv('NH_DEBUG') == "True":
        print("NH_DEBUG ---")
        import pydevd_pycharm

        pydevd_pycharm.settrace('127.0.0.1', port=9009, stdoutToServer=True, stderrToServer=True, suspend=False)
    
    # Register signal handlers.
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)
    
    # Register the cleanup function.
    atexit.register(cleanup_processes)
    
    # Start the Kafka consumer process.
    print("开始启动 Kafka 消费者进程...")  # Console output.
    StandLogger.info_log("准备启动 Kafka 消费者进程")
    
    if not start_kafka_consumer_process():
        print("Kafka 消费者进程启动失败")  # Console output.
        StandLogger.warn("Kafka 消费者进程启动失败，但继续启动 API 服务")
    else:
        print("Kafka 消费者进程启动成功")  # Console output.
        StandLogger.info_log("Kafka 消费者进程启动成功")
    StandLogger.info_log(f"所有缓存的配额模型:--- {quota_config_cache_tree.list_all_model_ids()}")
    StandLogger.info_log("启动 API 服务")
    uvicorn.run(app='main:app', host='0.0.0.0', port=base_config.PORTDEFAULT, limit_concurrency=500, reload=False)
