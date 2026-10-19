"""
Stress test: ramps up users in steps until the server struggles.

Every STEP_SECONDS, add STEP_USERS more StressUsers (no think time), up to MAX_USERS.
Watch the Locust charts for the point where RPS stops increasing while latency
(p95) climbs and failures appear: that is the saturation point.

Env vars: STEP_USERS (default 50), STEP_SECONDS (default 45), MAX_USERS (default 500)
"""
import os

from locust import LoadTestShape

from locustfile import StressUser  # noqa: F401  (only this user class is used here)

STEP_USERS = int(os.getenv("STEP_USERS", "50"))
STEP_SECONDS = int(os.getenv("STEP_SECONDS", "20"))
MAX_USERS = int(os.getenv("MAX_USERS", "300"))


class StepLoadShape(LoadTestShape):
    def tick(self):
        run_time = self.get_run_time()
        step = int(run_time // STEP_SECONDS) + 1
        users = step * STEP_USERS
        if users > MAX_USERS:
            if run_time > (MAX_USERS // STEP_USERS + 1) * STEP_SECONDS:
                return None  # stop the test after holding the max for one step
            users = MAX_USERS
        return users, STEP_USERS
