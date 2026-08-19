#!/bin/bash
# Sandbox Control Plane start script

# Set the project root directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export PYTHONPATH="$SCRIPT_DIR"

# Enter the project directory
cd "$SCRIPT_DIR"

# Clean up old processes on port 8000
echo "检查端口 8000..."
OLD_PIDS=$(lsof -ti:8000 2>/dev/null)
if [ -n "$OLD_PIDS" ]; then
    echo "发现旧进程占用端口 8000: $OLD_PIDS"
    echo "正在终止旧进程..."
    lsof -ti:8000 | xargs kill -9 2>/dev/null
    sleep 1
    echo "旧进程已清理"
fi

# Check the .env file
if [ ! -f .env ]; then
    echo "警告: .env 文件不存在"
    echo "正在从 .env.example 创建 .env..."
    cp .env.example .env
    echo "请根据需要编辑 .env 文件"
fi

# Sync dependencies
echo "正在同步依赖..."
if command -v uv &> /dev/null; then
    uv sync
else
    echo "uv 未安装，使用 pip 安装依赖..."
    pip install -e ".[dev]"
fi

# Start the service
echo "正在启动 Sandbox Control Plane..."
echo "提示: 使用 Ctrl+C 停止服务"
echo ""
echo "服务将在以下地址可用:"
echo "  - HTTP: http://localhost:8000"
echo "  - API 文档: http://localhost:8000/docs"
echo "  - ReDoc: http://localhost:8000/redoc"
echo ""

# Use uv or run directly
if command -v uv &> /dev/null; then
    uv run uvicorn src.interfaces.rest.main:app --host 0.0.0.0 --port 8000 --reload
else
    uvicorn src.interfaces.rest.main:app --host 0.0.0.0 --port 8000 --reload
fi
