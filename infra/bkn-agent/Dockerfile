FROM python:3.12-slim

ARG PIP_INDEX_URL=https://pypi.org/simple
WORKDIR /service

COPY requirements.lock .
RUN pip install --no-cache-dir --require-hashes -i ${PIP_INDEX_URL} -r requirements.lock

COPY VERSION .
COPY main.py .
COPY app ./app

EXPOSE 30800
CMD ["python", "main.py"]
