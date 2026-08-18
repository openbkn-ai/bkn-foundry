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
            StandLogger.info_log("Background task: preparing to start the Kafka consumer process")
            ok = start_kafka_consumer_process()
            if ok:
                StandLogger.info_log("Background task: Kafka consumer process started successfully")
            else:
                StandLogger.warn("Background task: Kafka consumer process failed to start; business APIs remain available")
        else:
            StandLogger.info_log("Background task: Kafka consumer process is already running; skipping startup")
    except Exception as e:
        StandLogger.error(f"Background task: error starting Kafka consumer process: {e}")


@app.on_event("shutdown")
async def _shutdown_kafka_consumer():
    try:
        StandLogger.info_log("FastAPI shutdown event: cleaning up the Kafka consumer process")
        cleanup_processes()
    except Exception as e:
        StandLogger.error(f"FastAPI shutdown event: error cleaning up Kafka consumer process: {e}")


def signal_handler(signum, frame):
    """Handle signals by shutting down all processes gracefully."""
    StandLogger.info_log(f"Received signal {signum}; shutting down all processes...")
    
    # Shut down the Kafka consumer process.
    if kafka_consumer_process and kafka_consumer_process.poll() is None:
        StandLogger.info_log("Shutting down Kafka consumer process...")
        kafka_consumer_process.terminate()
        try:
            kafka_consumer_process.wait(timeout=10)  # Wait for up to 10 seconds.
        except subprocess.TimeoutExpired:
            StandLogger.warn("Kafka consumer process did not stop within 10 seconds; forcing termination")
            kafka_consumer_process.kill()
    
    StandLogger.info_log("All processes have stopped")
    sys.exit(0)


def cleanup_processes():
    """Clean up subprocesses when the application exits."""
    if kafka_consumer_process and kafka_consumer_process.poll() is None:
        StandLogger.info_log("Cleaning up Kafka consumer process...")
        kafka_consumer_process.terminate()


def start_kafka_consumer_process():
    """Start the Kafka consumer subprocess."""
    global kafka_consumer_process
    
    try:
        print("Starting Kafka consumer subprocess...")  # Console output.
        StandLogger.info_log("Starting Kafka consumer subprocess...")
        
        # Build the command.
        script_path = os.path.join(os.path.dirname(__file__), 'kafka_consumer_process.py')
        print(f"Script path: {script_path}")  # Console output.
        print(f"Python interpreter: {sys.executable}")  # Console output.
        
        # Start an independent Kafka consumer process with subprocess.
        # Do not use PIPE so subprocess output is written directly to the console.
        kafka_consumer_process = subprocess.Popen([
            sys.executable, 
            script_path
        ], 
        cwd=os.path.dirname(__file__))
        
        print(f"Kafka consumer process started, PID: {kafka_consumer_process.pid}")  # Console output.
        StandLogger.info_log(f"Kafka consumer process started, PID: {kafka_consumer_process.pid}")
        
        # Check whether the process started successfully.
        print("Waiting for process startup...")  # Console output.
        time.sleep(2)  # Allow two seconds for startup.
        
        if kafka_consumer_process.poll() is not None:
            # The process has already exited.
            print("Process exited; startup may have failed")  # Console output.
            StandLogger.error("Kafka consumer process failed to start and has exited")
            return False
        else:
            print("Process is still running")  # Console output.
            
        return True
        
    except Exception as e:
        print(f"Error starting Kafka consumer process: {e}")  # Console output.
        StandLogger.error(f"Error starting Kafka consumer process: {e}")
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
    print("Starting Kafka consumer process...")  # Console output.
    StandLogger.info_log("Preparing to start Kafka consumer process")
    
    if not start_kafka_consumer_process():
        print("Kafka consumer process failed to start")  # Console output.
        StandLogger.warn("Kafka consumer process failed to start; continuing to start the API service")
    else:
        print("Kafka consumer process started successfully")  # Console output.
        StandLogger.info_log("Kafka consumer process started successfully")
    StandLogger.info_log(f"All cached quota models:--- {quota_config_cache_tree.list_all_model_ids()}")
    StandLogger.info_log("Starting API service")
    uvicorn.run(app='main:app', host='0.0.0.0', port=base_config.PORTDEFAULT, limit_concurrency=500, reload=False)
