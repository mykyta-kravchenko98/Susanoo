FROM python:3.12

RUN apt-get update && apt-get install -y --no-install-recommends \
    tesseract-ocr \
    tesseract-ocr-deu \
    tesseract-ocr-eng \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /var/task

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt awslambdaric

COPY app.py .

ENTRYPOINT ["python", "-m", "awslambdaric"]
CMD ["app.handler"]