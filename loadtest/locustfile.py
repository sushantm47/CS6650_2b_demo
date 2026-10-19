"""
Load test for the Product API.

Request mix (read-heavy, like a real store where people browse far more than staff edit):
  85%  GET  /products/{id}          existing product  -> expect 200
   5%  GET  /products/{id}          unknown product   -> expect 404 (counted as success here)
  10%  POST /products/{id}/details  update existing   -> expect 204

User classes (pick one on the command line / via USER_CLASSES):
  ProductUser      HttpUser     (python-requests)
  FastProductUser  FastHttpUser (geventhttpclient)
"""
import os
import random

from locust import FastHttpUser, HttpUser, TaskSet, between, task

SEED_PRODUCTS = int(os.getenv("SEED_PRODUCTS", "1000"))
WAIT_MIN = float(os.getenv("WAIT_MIN", "0.5"))
WAIT_MAX = float(os.getenv("WAIT_MAX", "1.5"))


class ProductTasks(TaskSet):
    @task(85)
    def get_existing(self):
        pid = random.randint(1, SEED_PRODUCTS)
        self.client.get(f"/products/{pid}", name="GET /products/{id}")

    @task(5)
    def get_missing(self):
        pid = random.randint(SEED_PRODUCTS + 1, SEED_PRODUCTS * 10)
        with self.client.get(f"/products/{pid}", name="GET /products/{id} (missing)", catch_response=True) as r:
            if r.status_code == 404:
                r.success()
            else:
                r.failure(f"expected 404, got {r.status_code}")

    @task(10)
    def post_details(self):
        pid = random.randint(1, SEED_PRODUCTS)
        body = {
            "product_id": pid,
            "sku": f"SKU-{pid:05d}",
            "manufacturer": random.choice(["Acme Corporation", "Globex", "Initech"]),
            "category_id": random.randint(1, 50),
            "weight": random.randint(0, 5000),
            "some_other_id": random.randint(1, 10_000),
        }
        self.client.post(f"/products/{pid}/details", json=body, name="POST /products/{id}/details")


class ProductUser(HttpUser):
    tasks = [ProductTasks]
    wait_time = between(WAIT_MIN, WAIT_MAX)


class FastProductUser(FastHttpUser):
    tasks = [ProductTasks]
    wait_time = between(WAIT_MIN, WAIT_MAX)


class StressUser(FastHttpUser):
    """No think time: every user sends requests back-to-back. Used by locustfile_stress.py."""
    tasks = [ProductTasks]
    wait_time = between(0, 0)
