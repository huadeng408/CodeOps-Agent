"""Final proxy test - isolate Python SSL + Clash issue"""
import os
os.environ["HTTPS_PROXY"] = "http://127.0.0.1:7890"
os.environ["HTTP_PROXY"] = "http://127.0.0.1:7890"
# CRITICAL: clear NO_PROXY to ensure raw.githubusercontent.com routes through proxy
for key in list(os.environ.keys()):
    if key.lower() in ("no_proxy", "noproxy"):
        del os.environ[key]

import requests

url = "https://raw.githubusercontent.com/django/django/4fc35a9c3efdc9154efce28cb23cb84f8834517e/tests/requirements/py3.txt"

# Test A: requests + verify=False + explicit proxy
print("=== Test A: explicit proxy dict + verify=False ===")
try:
    r = requests.get(url, proxies={"https": "http://127.0.0.1:7890"}, verify=False, timeout=15)
    print(f"OK: {r.status_code}, len={len(r.text)}")
except Exception as e:
    print(f"FAIL: {type(e).__name__}: {e}")

# Test B: use urllib3 directly
print("\n=== Test B: urllib3 ProxyManager + no cert ===")
import urllib3
urllib3.disable_warnings()
try:
    proxy = urllib3.ProxyManager(
        "http://127.0.0.1:7890",
        cert_reqs='CERT_NONE',
        assert_hostname=False,
    )
    r = proxy.request("GET", url, timeout=15.0)
    print(f"OK: {r.status}, len={len(r.data)}")
except Exception as e:
    print(f"FAIL: {type(e).__name__}: {e}")

# Test C: HTTP-only test to confirm proxy works at all
print("\n=== Test C: HTTP site through proxy ===")
try:
    r = requests.get("http://httpbin.org/ip", proxies={"http": "http://127.0.0.1:7890"}, timeout=15)
    print(f"OK: {r.status_code}, body={r.text.strip()}")
except Exception as e:
    print(f"FAIL: {type(e).__name__}: {e}")

# Test D: Check Python SSL and requests versions
print(f"\n=== Versions ===")
print(f"requests: {requests.__version__}")
print(f"urllib3: {urllib3.__version__}")
import ssl
print(f"OpenSSL: {ssl.OPENSSL_VERSION}")
print(f"Python: {__import__('sys').version}")

# Test E: urllib3 with cert_reqs=NONE + env proxy
print("\n=== Test E: urllib3 via env + no certs ===")
try:
    import urllib3
    http = urllib3.PoolManager(cert_reqs='CERT_NONE')
    r = http.request("GET", url, timeout=15.0)
    print(f"OK (direct, no proxy): {r.status}, len={len(r.data)}")
except Exception as e:
    print(f"FAIL: {type(e).__name__}: {e}")
