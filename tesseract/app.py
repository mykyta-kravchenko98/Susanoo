import io
import json
import logging
import os

import boto3
import pytesseract
from PIL import Image
from pytesseract import Output

logger = logging.getLogger()
logger.setLevel(logging.INFO)

s3 = boto3.client("s3")
sqs = boto3.client("sqs")

DOCUMENTS_BUCKET = os.environ["DOCUMENTS_BUCKET"]
PROCESSED_IMAGES_QUEUE_URL = os.environ["PROCESSED_IMAGES_QUEUE_URL"]

MAX_DIMENSION = 2000
JPEG_QUALITY = 82

MIN_ORIENTATION_CONFIDENCE = 0.03


def handler(event, context):
    for record in event["Records"]:
        body = json.loads(record["body"])
        chat_id = body["chat_id"]
        raw_keys = body["raw_keys"]

        processed_keys = [process_image(raw_key) for raw_key in raw_keys]

        sqs.send_message(
            QueueUrl=PROCESSED_IMAGES_QUEUE_URL,
            MessageBody=json.dumps({"chat_id": chat_id, "processed_keys": processed_keys}),
        )
        logger.info("processed letter chat_id=%s pages=%d", chat_id, len(processed_keys))


def process_image(raw_key: str) -> str:
    obj = s3.get_object(Bucket=DOCUMENTS_BUCKET, Key=raw_key)
    image_bytes = obj["Body"].read()

    img = Image.open(io.BytesIO(image_bytes))
    img = img.convert("RGB")

    rotation = detect_rotation(img)
    if rotation:
        img = img.rotate(-rotation, expand=True)

    img = downsample(img)

    processed_key = raw_key.replace("raw/", "processed/", 1)
    buffer = io.BytesIO()
    img.save(buffer, format="JPEG", quality=JPEG_QUALITY)
    s3.put_object(
        Bucket=DOCUMENTS_BUCKET,
        Key=processed_key,
        Body=buffer.getvalue(),
        ContentType="image/jpeg",
    )

    return processed_key


def detect_rotation(img: Image.Image) -> int:
    try:
        osd = pytesseract.image_to_osd(img, output_type=Output.DICT)
    except pytesseract.TesseractError as e:
        logger.warning("tesseract OSD failed, skipping rotation: %s", e)
        return 0

    confidence = float(osd.get("orientation_conf", 0))
    suggested_rotation = int(osd.get("rotate", 0))
    logger.info(
        "tesseract osd result: confidence=%.3f suggested_rotation=%d",
        confidence, suggested_rotation,
    )

    if confidence < MIN_ORIENTATION_CONFIDENCE:
        logger.info("orientation confidence too low (%.2f), skipping rotation", confidence)
        return 0

    return suggested_rotation


def downsample(img: Image.Image) -> Image.Image:
    width, height = img.size
    longest_side = max(width, height)
    if longest_side <= MAX_DIMENSION:
        return img

    scale = MAX_DIMENSION / longest_side
    new_size = (int(width * scale), int(height * scale))
    return img.resize(new_size, Image.LANCZOS)