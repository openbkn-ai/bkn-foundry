FROM python:3.12-slim

ARG PIP_INDEX_URL=https://pypi.org/simple
WORKDIR /service

COPY requirements.txt .
RUN pip install --no-cache-dir -i ${PIP_INDEX_URL} -r requirements.txt

COPY VERSION .
COPY main.py .
COPY app ./app

EXPOSE 30800
CMD ["python", "main.py"]
