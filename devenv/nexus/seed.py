#!/usr/bin/env python3
"""Seed a throwaway Nexus Repository for the dev stack and the tests.

Runs once per `docker compose up`, and is safe to run again. It:

  * sets the admin password (Nexus generates a random one on first start),
  * accepts the Community Edition licence, which the API requires,
  * creates a blob store with a quota, a few repositories and sample
    artifacts with several versions,
  * creates two users for Grafana and writes their passwords to SEED_OUT:
      password          user "grafana": every privilege the plugin can use
      limited-password  user "grafana-limited": browse and search only

Only the standard library is used, so any Python image can run it.
"""

import base64
import hashlib
import json
import os
import secrets
import sys
import urllib.error
import urllib.request

URL = os.environ.get("NEXUS_URL", "http://localhost:8081").rstrip("/")
ADMIN_PASSWORD = os.environ.get("NEXUS_ADMIN_PASSWORD", "admin-dev-only")
OUT = os.environ.get("SEED_OUT", "/seed-out")
FIRST_PASSWORD_FILE = "/nexus-data/admin.password"
REST = URL + "/service/rest/v1"

READER_PRIVILEGES = [
    "nx-repository-view-*-*-browse",
    "nx-repository-view-*-*-read",
    "nx-repository-admin-*-*-read",
    "nx-search-read",
    "nx-blobstores-read",
    "nx-tasks-read",
    "nx-metrics-all",
]
LIMITED_PRIVILEGES = ["nx-repository-view-*-*-browse", "nx-repository-view-*-*-read", "nx-search-read"]


def call(method, url, user="admin", password=None, body=None, content_type="application/json"):
    """Return (status, body bytes). HTTP errors are returned, not raised."""
    data = None
    if body is not None:
        data = body if isinstance(body, bytes) else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method)
    if user:
        token = base64.b64encode(f"{user}:{password or ADMIN_PASSWORD}".encode()).decode()
        req.add_header("Authorization", "Basic " + token)
    if data is not None:
        req.add_header("Content-Type", content_type)
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as err:
        return err.code, err.read()


def must(status, what, ok=(200, 201, 204)):
    if status not in ok:
        sys.exit(f"seed: {what} failed with HTTP {status}")


def set_admin_password():
    status, _ = call("GET", REST + "/repositories")
    if status == 200:
        return
    try:
        with open(FIRST_PASSWORD_FILE, encoding="utf-8") as f:
            first = f.read().strip()
    except OSError:
        sys.exit("seed: the admin password is neither NEXUS_ADMIN_PASSWORD nor the generated one. "
                 "Remove the nexus-data volume to start again.")
    status, _ = call("PUT", REST + "/security/users/admin/change-password", password=first,
                     body=ADMIN_PASSWORD.encode(), content_type="text/plain")
    must(status, "setting the admin password")


def accept_licence():
    status, body = call("GET", REST + "/system/eula")
    if status == 404:
        return  # Versions before Community Edition have no licence step.
    must(status, "reading the licence status")
    eula = json.loads(body)
    if not eula.get("accepted"):
        eula["accepted"] = True
        status, _ = call("POST", REST + "/system/eula", body=eula)
        must(status, "accepting the licence")


def create(path, body, what):
    """Create something; 400 means it already exists."""
    status, _ = call("POST", REST + path, body=body)
    must(status, what, ok=(200, 201, 204, 400))


def create_storage_and_repositories():
    create("/blobstores/file", {
        "name": "artifacts",
        "path": "artifacts",
        "softQuota": {"type": "spaceUsedQuota", "limit": 10 * 1024 ** 3},
    }, "creating the blob store")
    storage = {"blobStoreName": "artifacts", "strictContentTypeValidation": False, "writePolicy": "ALLOW"}
    create("/repositories/raw/hosted", {"name": "raw-hosted", "online": True, "storage": storage},
           "creating raw-hosted")
    create("/repositories/raw/hosted", {
        "name": "docs-archive", "online": False, "storage": dict(storage, writePolicy="DENY"),
    }, "creating docs-archive")
    create("/repositories/npm/proxy", {
        "name": "npm-proxy",
        "online": True,
        "storage": {"blobStoreName": "default", "strictContentTypeValidation": True},
        "proxy": {"remoteUrl": "https://registry.npmjs.org", "contentMaxAge": 1440, "metadataMaxAge": 1440},
        "negativeCache": {"enabled": True, "timeToLive": 1440},
        "httpClient": {"blocked": False, "autoBlock": True},
    }, "creating npm-proxy")


def content(name, size):
    """Deterministic bytes, so a second run uploads identical files."""
    out = b""
    counter = 0
    while len(out) < size:
        out += hashlib.sha256(f"{name}:{counter}".encode()).digest()
        counter += 1
    return out[:size]


def upload(repository, path, data):
    status, _ = call("PUT", f"{URL}/repository/{repository}/{path}", body=data,
                     content_type="application/octet-stream")
    # 400 or 409: the release already exists and may not be redeployed.
    must(status, f"uploading {path}", ok=(200, 201, 204, 400, 409))


MAVEN = {
    ("com.example.shop", "billing"): ["1.0.0", "1.1.0", "1.2.0", "2.0.0"],
    ("com.example.shop", "checkout"): ["0.9.0", "1.0.0"],
    ("com.example.platform", "auth-client"): ["3.4.1", "3.5.0", "3.5.1"],
}
RAW = {
    "tools/cli": ["1.1.0", "1.2.0", "1.3.0"],
    "tools/installer": ["2025.10", "2025.11"],
}


def upload_artifacts():
    n = 0
    for (group, artifact), versions in MAVEN.items():
        for version in versions:
            n += 1
            base = f"{group.replace('.', '/')}/{artifact}/{version}/{artifact}-{version}"
            pom = (f"<project><modelVersion>4.0.0</modelVersion><groupId>{group}</groupId>"
                   f"<artifactId>{artifact}</artifactId><version>{version}</version></project>")
            upload("maven-releases", base + ".pom", pom.encode())
            upload("maven-releases", base + ".jar", content(base, 20_000 + n * 7_000))
    for prefix, versions in RAW.items():
        name = prefix.split("/")[-1]
        for version in versions:
            n += 1
            path = f"{prefix}/{version}/{name}-{version}.bin"
            upload("raw-hosted", path, content(path, 50_000 + n * 11_000))
    # Downloads set "last downloaded" and feed the request counts.
    for path in ("maven-releases/com/example/shop/billing/2.0.0/billing-2.0.0.jar",
                 "maven-releases/com/example/platform/auth-client/3.5.1/auth-client-3.5.1.jar",
                 "raw-hosted/tools/cli/1.3.0/cli-1.3.0.bin"):
        status, _ = call("GET", f"{URL}/repository/{path}")
        must(status, f"downloading {path}")


def create_user(user, role, privileges, password_file):
    path = os.path.join(OUT, password_file)
    try:
        with open(path, encoding="utf-8") as f:
            password = f.read().strip()
    except OSError:
        password = secrets.token_urlsafe(24)
    role_body = {"id": role, "name": role, "description": "Read access for Grafana",
                 "privileges": privileges, "roles": []}
    status, _ = call("PUT", f"{REST}/security/roles/{role}", body=role_body)
    if status == 404:
        status, _ = call("POST", REST + "/security/roles", body=role_body)
    must(status, f"creating the role {role}")

    status, _ = call("POST", REST + "/security/users", body={
        "userId": user, "firstName": "Grafana", "lastName": "Reader",
        "emailAddress": f"{user}@example.com", "password": password,
        "status": "active", "roles": [role],
    })
    if status not in (200, 201, 204):
        # The user exists: make sure the stored password is the current one.
        status, _ = call("PUT", f"{REST}/security/users/{user}/change-password",
                         body=password.encode(), content_type="text/plain")
        must(status, f"creating the user {user}")
    with open(path, "w", encoding="utf-8") as f:
        f.write(password)
    os.chmod(path, 0o644)


def main():
    os.makedirs(OUT, exist_ok=True)
    set_admin_password()
    accept_licence()
    create_storage_and_repositories()
    upload_artifacts()
    create_user("grafana", "grafana-reader", READER_PRIVILEGES, "password")
    create_user("grafana-limited", "grafana-limited", LIMITED_PRIVILEGES, "limited-password")
    print("seed: Nexus is ready")


if __name__ == "__main__":
    main()
