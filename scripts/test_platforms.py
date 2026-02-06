#!/usr/bin/env python3
import argparse
import json
import sys
import time
import urllib.error
import urllib.request


def request_json(method, url, data=None, headers=None, timeout=20):
    headers = headers or {}
    body = None
    if data is not None:
        body = json.dumps(data).encode("utf-8")
        headers.setdefault("Content-Type", "application/json")
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8")
            return resp.status, json.loads(raw)
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8")
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, {"error": raw}
    except urllib.error.URLError as e:
        return 0, {"error": str(e)}


def create_job(api_base, url):
    status, payload = request_json("POST", f"{api_base}/jobs", {"url": url})
    if status != 202:
        raise RuntimeError(payload.get("error", f"unexpected status {status}"))
    return payload["job_id"]


def get_job(api_base, job_id):
    status, payload = request_json("GET", f"{api_base}/jobs/{job_id}")
    if status != 200:
        raise RuntimeError(payload.get("error", f"unexpected status {status}"))
    return payload


def wait_job(api_base, job_id, interval, timeout):
    deadline = time.time() + timeout
    while True:
        job = get_job(api_base, job_id)
        status = job.get("status")
        if status in {"ready", "failed", "expired"}:
            return job
        if time.time() >= deadline:
            job["status"] = "timeout"
            return job
        time.sleep(interval)


def load_urls(path):
    if path == "-":
        content = sys.stdin.read()
    else:
        with open(path, "r", encoding="utf-8") as f:
            content = f.read()
    return [line.strip() for line in content.splitlines() if line.strip() and not line.strip().startswith("#")]


def main():
    parser = argparse.ArgumentParser(description="Batch test video2mp3 platforms")
    parser.add_argument("-f", "--file", required=True, help="URL list file (one URL per line, # for comments)")
    parser.add_argument("--api", default="http://localhost:8080", help="API base URL")
    parser.add_argument("--interval", type=float, default=2.0, help="Polling interval seconds")
    parser.add_argument("--timeout", type=int, default=180, help="Timeout seconds per job")
    args = parser.parse_args()

    api_base = args.api.rstrip("/")
    urls = load_urls(args.file)
    if not urls:
        print("no urls provided", file=sys.stderr)
        return 1

    print(f"API: {api_base}")
    for url in urls:
        print(f"\n==> {url}")
        try:
            job_id = create_job(api_base, url)
            print(f"job_id: {job_id}")
            job = wait_job(api_base, job_id, args.interval, args.timeout)
            status = job.get("status")
            print(f"status: {status}")
            if status == "ready":
                print(f"download: {api_base}/jobs/{job_id}/download")
            elif status == "failed":
                print(f"error: {job.get('error')}")
        except Exception as e:
            print(f"error: {e}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
