FROM python:3.13-slim

WORKDIR /app

COPY tests/openshell-grpc-mock/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY tests/openshell-grpc-mock/server.py .
COPY src/python/generated /app/src/python/generated
COPY proto /app/proto

EXPOSE 50051

CMD ["python", "server.py"]
