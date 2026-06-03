ARG BASE_IMAGE=ubuntu:24.04
FROM ${BASE_IMAGE}

WORKDIR /app

COPY server ./server

RUN set -ex; \
  ARCH=$(dpkg --print-architecture); \
  echo "检测到当前架构为: $ARCH"; \
  cp /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list.d/ubuntu.sources.bak; \
  \
  if [ "$ARCH" = "arm64" ]; then \
    echo "正在切换为阿里云 ARM/Ports 源..."; \
    # 替换 ports.ubuntu.com/ubuntu-ports 为 mirrors.aliyun.com/ubuntu-ports
    sed -i 's|http://ports.ubuntu.com/ubuntu-ports|http://mirrors.aliyun.com/ubuntu-ports|g' /etc/apt/sources.list.d/ubuntu.sources; \
    sed -i 's|http://ports.ubuntu.com|http://mirrors.aliyun.com/ubuntu-ports|g' /etc/apt/sources.list.d/ubuntu.sources; \
  else \
    echo "正在切换为阿里云 x86_64 源..."; \
    # 替换 archive.ubuntu.com 和 security.ubuntu.com 为 mirrors.aliyun.com/ubuntu
    sed -i 's|http://archive.ubuntu.com|http://mirrors.aliyun.com|g' /etc/apt/sources.list.d/ubuntu.sources; \
    sed -i 's|http://security.ubuntu.com|http://mirrors.aliyun.com|g' /etc/apt/sources.list.d/ubuntu.sources; \
  fi; \
  \
  apt-get update; \
  apt-get install -y git python3-pip; \
  pip3 config set global.index-url http://mirrors.aliyun.com/pypi/simple/; \
  pip3 config set install.trusted-host mirrors.aliyun.com; \
  pip3 install -r server/requirements.txt --break-system-packages; \
  apt-get clean; \
  rm -rf /var/lib/apt/lists/;

RUN set -ex; \
    echo "/usr/local/lib/python3.12/dist-packages/ksycopg2" > /etc/ld.so.conf.d/ksycopg2.conf; \
    ldconfig;

CMD ["python3", "server/data-migrator.py"]
