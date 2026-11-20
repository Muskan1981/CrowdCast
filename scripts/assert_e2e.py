#!/usr/bin/env python3
"""Assertions for scripts/e2e.sh. Reads the loadgen summaries and exits non-zero on failure.

Checks:
  * the failure phase's HTTP error ratio is under --max-error-pct
  * the killed node's share of responses in the failure phase is near zero (under 2%)
  * the remaining nodes take the traffic (their combined share is above 98%)
  * the killed node came back: the fleet view reports every node Healthy
"""
import argparse
import json
import pathlib
import sys
import urllib.request


def load(path):
    with open(path) as f:
        return json.load(f)


def wait_for_healthy(app, deadline_s):
    """Poll the fleet view until every node reports Healthy or the deadline passes."""
    import time
    end = time.time() + deadline_s
    statuses, fleet_status, err = {}, "", None
    while True:
        try:
            with urllib.request.urlopen(f"{app}/api/v1/infra/nodes", timeout=5) as resp:
                fleet = json.load(resp)
            statuses = {r["name"]: r["status"] for r in fleet.get("server_rows", [])}
            fleet_status = fleet.get("fleet_status", "")
            err = None
            if statuses and all(s == "Healthy" for s in statuses.values()):
                return statuses, fleet_status, None
        except Exception as exc:  # noqa: BLE001 - any failure to reach the fleet view is reported
            err = str(exc)
        if time.time() >= end:
            return statuses, fleet_status, err
        time.sleep(1)


def error_pct(summary):
    reqs = summary.get("http_requests", 0)
    if reqs == 0:
        return None
    return 100.0 * summary.get("http_errors", 0) / reqs


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("out_dir")
    ap.add_argument("--max-error-pct", type=float, default=2.0)
    ap.add_argument("--killed", required=True)
    ap.add_argument("--app", required=True)
    ap.add_argument("--admin-key", default="")
    args = ap.parse_args()

    out = pathlib.Path(args.out_dir)
    baseline = load(out / "baseline.json")
    failure = load(out / "failure.json")
    failures = []
    report = []

    # 1. Error ratio during the failure phase.
    pct = error_pct(failure)
    report.append(f"failure phase: {failure['http_requests']} HTTP requests, "
                  f"{failure['http_errors']} errors ({pct if pct is not None else 0:.2f}%), "
                  f"reconnects {failure['reconnects']} (close 1012: {failure['reconnects_1012']})")
    if pct is None:
        failures.append("failure phase made no HTTP requests")
    elif pct >= args.max_error_pct:
        failures.append(f"error ratio {pct:.2f}% is not below {args.max_error_pct}%")

    # 2. The killed node's share, before and during the failure.
    def share(summary, node):
        return summary.get("served_by_pct", {}).get(node, 0.0)

    before = share(baseline, args.killed)
    during = share(failure, args.killed)
    report.append(f"{args.killed} share: baseline {before:.1f}%, during failure {during:.1f}%")
    if during >= 2.0:
        failures.append(f"{args.killed} still served {during:.1f}% of responses during the failure")
    if before <= 2.0:
        failures.append(f"{args.killed} served only {before:.1f}% before the failure; the baseline is not a fair comparison")

    # 3. The traffic re-balances to the other nodes.
    others = {n: p for n, p in failure.get("served_by_pct", {}).items() if n != args.killed}
    other_total = sum(others.values())
    report.append("remaining nodes during failure: " +
                  ", ".join(f"{n} {p:.1f}%" for n, p in sorted(others.items())))
    if other_total < 98.0:
        failures.append(f"remaining nodes only took {other_total:.1f}% of responses")

    # 4. The killed node is back. Heartbeats are every 2 s, so allow up to 20 s.
    statuses, fleet_status, err = wait_for_healthy(args.app, deadline_s=20)
    if err:
        failures.append(f"could not read the fleet view: {err}")
    else:
        report.append(f"fleet after restore: {fleet_status} {statuses}")
        bad = {n: s for n, s in statuses.items() if s != "Healthy"}
        if bad:
            failures.append(f"nodes not healthy after restore: {bad}")

    print("\n".join(report))
    if failures:
        print("\nFAIL")
        for f in failures:
            print(f"  - {f}")
        sys.exit(1)
    print("\nPASS")


if __name__ == "__main__":
    main()
