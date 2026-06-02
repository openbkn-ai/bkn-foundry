# 基于镜像基础（公网重建版，推 GHCR；可经 build-arg 覆盖）
ARG BASE_IMAGE=ghcr.io/kowell-ai/model-factory-base:v2
FROM ${BASE_IMAGE}
# 设置代码文件夹工作目录 /app
WORKDIR /app
# 复制当前代码文件到容器中 /app
ADD . /app

# 端口
EXPOSE 9898
# CMD
#CMD ["python3", "/app/main.py"]
ENTRYPOINT [ "uvicorn", "main:app", "--host", "0.0.0.0", "--port", "9898", "--workers", "1"]
