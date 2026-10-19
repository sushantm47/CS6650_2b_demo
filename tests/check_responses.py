"""
Sends one request per documented response code of the Product endpoints and
checks the status. Works against a local server or the ECS deployment.

Usage:  python tests/check_responses.py [BASE_URL]
        BASE_URL defaults to http://localhost:8080
Needs:  pip install requests
"""
import json
import sys

import requests

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080").rstrip("/")


def product(pid, **overrides):
    body = {"product_id": pid, "sku": "ABC-123-XYZ", "manufacturer": "Acme Corporation",
            "category_id": 456, "weight": 1250, "some_other_id": 789}
    body.update(overrides)
    return body


FAIL = {"X-Simulate-Error": "true"}
cases = [
    # name,                                          method, path,                   body,                        headers, expected
    ("GET existing product",                         "GET",  "/products/1",          None,                        None, 200),
    ("GET unknown product",                          "GET",  "/products/999999",     None,                        None, 404),
    ("GET non-numeric id",                           "GET",  "/products/abc",        None,                        None, 404),
    ("GET simulated server failure",                 "GET",  "/products/1",          None,                        FAIL, 500),
    ("POST valid details",                           "POST", "/products/1/details",  product(1),                  None, 204),
    ("POST malformed JSON",                          "POST", "/products/1/details",  "{not json",                 None, 400),
    ("POST missing required fields",                 "POST", "/products/1/details",  {"product_id": 1},           None, 400),
    ("POST negative weight",                         "POST", "/products/1/details",  product(1, weight=-5),       None, 400),
    ("POST empty sku",                               "POST", "/products/1/details",  product(1, sku=""),          None, 400),
    ("POST body id != path id",                      "POST", "/products/2/details",  product(1),                  None, 400),
    ("POST invalid path id (0)",                     "POST", "/products/0/details",  product(1),                  None, 400),
    ("POST unknown product",                         "POST", "/products/999999/details", product(999999),         None, 404),
    ("POST simulated server failure",                "POST", "/products/1/details",  product(1),                  FAIL, 500),
]

print(f"Target: {BASE}\n")
print(f"{'case':<34} {'request':<36} {'expected':>8} {'got':>4}  result  response")
failed = 0
for name, method, path, body, headers, expected in cases:
    kwargs = {"headers": headers or {}, "timeout": 10}
    if isinstance(body, str):
        kwargs["data"] = body
        kwargs["headers"] = {**kwargs["headers"], "Content-Type": "application/json"}
    elif body is not None:
        kwargs["json"] = body
    r = requests.request(method, BASE + path, **kwargs)
    ok = r.status_code == expected
    failed += not ok
    text = (r.text or "").strip().replace("\n", " ")
    print(f"{name:<34} {method + ' ' + path:<36} {expected:>8} {r.status_code:>4}  {'PASS' if ok else 'FAIL':<6}  {text[:70]}")

# show that the 204 update actually changed the stored product
r = requests.get(BASE + "/products/1", timeout=10)
print("\nGET /products/1 after update ->", r.status_code, json.dumps(r.json()))
print(f"\n{len(cases) - failed}/{len(cases)} cases passed")
sys.exit(1 if failed else 0)
