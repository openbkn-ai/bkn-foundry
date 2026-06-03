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
from app.utils.config_cache import quota_config_cache_tree  # 初始化大模型配置缓存

app = create_app()

# 全局变量存储 Kafka 消费者进程
kafka_consumer_process = None


# 在 FastAPI 启动/停止事件中管理 Kafka 消费者，确保在 uvicorn main:app 模式下也会启动。
#
# 关键约束：FastAPI lifespan startup 是 *同步* 阶段——所有 on_event("startup")
# handler 顺序 await 完成后，uvicorn 才会绑定端口并响应请求（含 /api/v1/health/ready）。
# start_kafka_consumer_process() 里有 time.sleep(2) 同步阻塞 + subprocess.Popen
# 可能进一步因 Kafka broker 握手慢而拖时间；放在 startup handler 里直接 await，
# 会让端口绑定推迟到 Kafka 子进程稳定后才发生，startup probe 早就探到
# connection refused -> CrashLoopBackOff。
#
# 改成 fire-and-forget：startup handler 立刻安排一个后台任务，handler 本身
# 立刻返回，uvicorn 接着完成 lifespan、绑定端口、响应 /health/ready。Kafka
# 消费者起不来不阻塞业务接口，符合 _check_and_create_topic 的现有降级语义。
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
    """信号处理器，用于优雅关闭所有进程"""
    StandLogger.info_log(f"收到信号 {signum}，开始关闭所有进程...")
    
    # 关闭 Kafka 消费者进程
    if kafka_consumer_process and kafka_consumer_process.poll() is None:
        StandLogger.info_log("正在关闭 Kafka 消费者进程...")
        kafka_consumer_process.terminate()
        try:
            kafka_consumer_process.wait(timeout=10)  # 等待最多10秒
        except subprocess.TimeoutExpired:
            StandLogger.warn("Kafka 消费者进程未能在10秒内关闭，强制终止")
            kafka_consumer_process.kill()
    
    StandLogger.info_log("所有进程已关闭")
    sys.exit(0)


def cleanup_processes():
    """清理函数，在程序退出时调用"""
    if kafka_consumer_process and kafka_consumer_process.poll() is None:
        StandLogger.info_log("清理 Kafka 消费者进程...")
        kafka_consumer_process.terminate()


def start_kafka_consumer_process():
    """启动 Kafka 消费者子进程"""
    global kafka_consumer_process
    
    try:
        print("正在启动 Kafka 消费者子进程...")  # 控制台输出
        StandLogger.info_log("启动 Kafka 消费者子进程...")
        
        # 构建命令
        script_path = os.path.join(os.path.dirname(__file__), 'kafka_consumer_process.py')
        print(f"脚本路径: {script_path}")  # 控制台输出
        print(f"Python 解释器: {sys.executable}")  # 控制台输出
        
        # 使用 subprocess 启动独立的 Kafka 消费者进程
        # 不使用 PIPE，让子进程的输出直接显示到控制台
        kafka_consumer_process = subprocess.Popen([
            sys.executable, 
            script_path
        ], 
        cwd=os.path.dirname(__file__))
        
        print(f"Kafka 消费者进程已启动，PID: {kafka_consumer_process.pid}")  # 控制台输出
        StandLogger.info_log(f"Kafka 消费者进程已启动，PID: {kafka_consumer_process.pid}")
        
        # 检查进程是否正常启动
        print("等待进程启动...")  # 控制台输出
        time.sleep(2)  # 等待2秒让进程启动
        
        if kafka_consumer_process.poll() is not None:
            # 进程已经退出
            print("进程已退出，可能启动失败")  # 控制台输出
            StandLogger.error("Kafka 消费者进程启动失败，进程已退出")
            return False
        else:
            print("进程仍在运行")  # 控制台输出
            
        return True
        
    except Exception as e:
        print(f"启动 Kafka 消费者进程时出错: {e}")  # 控制台输出
        StandLogger.error(f"启动 Kafka 消费者进程时出错: {e}")
        import traceback
        traceback.print_exc()  # 打印详细错误信息
        return False


if __name__ == '__main__':
    print(f"NH_DEBUG={os.getenv('NH_DEBUG')}")
    if os.getenv('NH_DEBUG') == "True":
        print("NH_DEBUG ---")
        import pydevd_pycharm

        pydevd_pycharm.settrace('127.0.0.1', port=9009, stdoutToServer=True, stderrToServer=True, suspend=False)
    
    # 注册信号处理器
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)
    
    # 注册清理函数
    atexit.register(cleanup_processes)
    
    # 启动 Kafka 消费者进程
    print("开始启动 Kafka 消费者进程...")  # 添加控制台输出
    StandLogger.info_log("准备启动 Kafka 消费者进程")
    
    if not start_kafka_consumer_process():
        print("Kafka 消费者进程启动失败")  # 添加控制台输出
        StandLogger.warn("Kafka 消费者进程启动失败，但继续启动 API 服务")
    else:
        print("Kafka 消费者进程启动成功")  # 添加控制台输出
        StandLogger.info_log("Kafka 消费者进程启动成功")
    StandLogger.info_log(f"所有缓存的配额模型:--- {quota_config_cache_tree.list_all_model_ids()}")
    StandLogger.info_log("启动 API 服务")
    uvicorn.run(app='main:app', host='0.0.0.0', port=base_config.PORTDEFAULT, limit_concurrency=500, reload=False)
