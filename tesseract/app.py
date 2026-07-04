import json

def handler(event, context):
    print(f"received event (not yet processed): {json.dumps(event)}")
    return {"status": "not_implemented_yet"}