#!/usr/bin/env python3
import sys
import time
import re
import html
import requests

"""
Fetches the PyPI Simple index and emits Redis RESP protocol commands to add
all project names into a Redis set named 'pypi:names'.

STDOUT: RESP commands suitable for piping to `redis-cli --pipe`
STDERR: Minimal progress logs
"""


def main() -> None:
    start_time = time.time()
    headers = {
        "User-Agent": "r-spack-recipe-builder/0.1 (+local import)",
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
    }
    url = "https://pypi.org/simple/"
    sys.stderr.write(f"Fetching {url}...\n")
    response = requests.get(url, headers=headers, timeout=120)
    response.raise_for_status()
    html_text = response.text
    fetch_secs = time.time() - start_time
    sys.stderr.write(f"Downloaded {len(html_text):,} bytes in {fetch_secs:.2f}s. Parsing...\n")

    # Extract anchor inner texts which are the normalized project names
    # The Simple index format is a list of <a href=...>name</a>
    anchor_texts = re.findall(r"<a[^>]*>([^<]+)</a>", html_text, flags=re.IGNORECASE)
    project_names = []
    seen = set()
    for raw in anchor_texts:
        name = html.unescape(raw).strip()
        if not name:
            continue
        if name in seen:
            continue
        seen.add(name)
        project_names.append(name)

    sys.stderr.write(f"Parsed {len(project_names):,} unique project names. Emitting RESP...\n")

    # Emit Redis protocol for SADD pypi:names <name>
    # *3\r\n$4\r\nSADD\r\n$10\r\npypi:names\r\n$<len>\r\n<name>\r\n
    out = sys.stdout
    for name in project_names:
        name_bytes_len = len(name.encode("utf-8"))
        out.write("*3\r\n")
        out.write("$4\r\nSADD\r\n")
        out.write("$10\r\npypi:names\r\n")
        out.write(f"${name_bytes_len}\r\n{name}\r\n")

    sys.stderr.write(
        f"Done. Wrote RESP for {len(project_names):,} project names in {time.time()-start_time:.2f}s.\n"
    )


if __name__ == "__main__":
    main()

