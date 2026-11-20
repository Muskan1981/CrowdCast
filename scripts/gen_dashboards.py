#!/usr/bin/env python3
"""Generates the five CrowdCast Grafana dashboards as JSON.

The JSON files are committed, so Grafana loads them with no build step. Run this
script only when you change a panel, then commit the regenerated files:

    python3 scripts/gen_dashboards.py
"""
import json
import pathlib

OUT = pathlib.Path(__file__).resolve().parents[1] / "deploy/grafana/dashboards"
DS = {"type": "prometheus", "uid": "prometheus"}
NODES = ["crowdcast-api-01", "crowdcast-api-02", "crowdcast-api-03"]


class Builder:
    def __init__(self, uid, title, description):
        self.uid = uid
        self.title = title
        self.description = description
        self.panels = []
        self.y = 0
        self.next_id = 1

    def _id(self):
        pid = self.next_id
        self.next_id += 1
        return pid

    def row(self, title):
        self.panels.append({"type": "row", "title": title, "id": self._id(),
                            "collapsed": False, "gridPos": {"x": 0, "y": self.y, "w": 24, "h": 1}, "panels": []})
        self.y += 1

    def _place(self, w, h, x):
        pos = {"x": x, "y": self.y, "w": w, "h": h}
        return pos

    def stats(self, items):
        """items: (title, expr, unit, [thresholds]) in one row, equal width."""
        n = len(items)
        w = 24 // n
        for i, (title, expr, unit, color) in enumerate(items):
            self.panels.append({
                "type": "stat", "title": title, "id": self._id(), "datasource": DS,
                "gridPos": self._place(w, 4, i * w),
                "targets": [{"refId": "A", "datasource": DS, "expr": expr, "legendFormat": "{{instance}}"}],
                "fieldConfig": {"defaults": {"unit": unit, "color": {"mode": "fixed", "fixedColor": color},
                                              "thresholds": {"mode": "absolute", "steps": [{"color": color, "value": None}]}},
                                "overrides": []},
                "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                            "graphMode": "area", "colorMode": "value", "textMode": "value_and_name"},
            })
        self.y += 4

    def series(self, title, targets, unit="short", w=12, h=8, x=0, kind="timeseries", description="", stack=False):
        """targets: list of (refId, expr, legend)."""
        tgs = [{"refId": r, "datasource": DS, "expr": e, "legendFormat": l} for r, e, l in targets]
        panel = {
            "type": kind, "title": title, "id": self._id(), "datasource": DS,
            "description": description, "gridPos": self._place(w, h, x), "targets": tgs,
            "fieldConfig": {"defaults": {"unit": unit, "custom": {"lineWidth": 2, "fillOpacity": 12,
                                                                  "stacking": {"mode": "normal" if stack else "none"}}},
                            "overrides": []},
            "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}},
        }
        self.panels.append(panel)
        return panel

    def place_row(self, panels_w):
        """Place a list of series panels side by side, each (w, h) pair."""
        x = 0
        for p, (w, h) in panels_w:
            p["gridPos"] = {"x": x, "y": self.y, "w": w, "h": h}
            x += w
        self.y += max(h for _, (_, h) in panels_w)

    def build(self, tags, annotations=None, refresh="5s"):
        templating = {"list": [{
            "name": "instance", "label": "Instance", "type": "query", "datasource": DS,
            "query": {"query": "label_values(http_requests_total, instance)", "refId": "q"},
            "refresh": 2, "includeAll": True, "multi": True, "allValue": ".*",
            "current": {"text": "All", "value": "$__all"},
        }]}
        return {
            "uid": self.uid, "title": self.title, "description": self.description,
            "tags": ["crowdcast"] + tags, "timezone": "browser", "schemaVersion": 39,
            "version": 1, "refresh": refresh, "time": {"from": "now-30m", "to": "now"},
            "editable": False, "graphTooltip": 1, "style": "dark",
            "templating": templating,
            "annotations": {"list": annotations or []},
            "panels": self.panels,
        }


def chaos_annotations():
    return [{
        "name": "Chaos events", "datasource": DS, "enable": True, "iconColor": "orange",
        "expr": "changes(chaos_events_total[30s]) > 0", "step": "30s", "useValueForTime": False,
        "titleFormat": "{{node}}: {{action}}", "textFormat": "Chaos command applied",
    }]


def overview():
    b = Builder("crowdcast-overview", "CrowdCast Overview",
                "Live health of the whole fleet: throughput, latency, errors and audience.")
    b.row("Headline")
    b.stats([
        ("Requests / s", "sum(crowdcast:http_requests:total_rate1m)", "reqps", "#73BF69"),
        ("p95 latency", 'histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{route!="unmatched"}[5m])))', "s", "#5794F2"),
        ("Error rate", "100 * (crowdcast:http_error_ratio:rate5m_total or vector(0))", "percent", "#FF9830"),
        ("Active WebSockets", "sum(ws_connections_active)", "short", "#B877D9"),
        ("Audience", "max(event_audience)", "short", "#FADE2A"),
    ])
    b.row("Trends")
    p1 = b.series("Requests / s by instance", [("A", "crowdcast:http_requests:rate1m", "{{instance}}")], "reqps", w=12, h=8)
    p2 = b.series("p95 latency by instance", [("A", "crowdcast:http_latency:p95_5m", "{{instance}}")], "s", w=12, h=8)
    b.place_row([(p1, (12, 8)), (p2, (12, 8))]); b.y = b.y
    p3 = b.series("Error ratio", [("A", "crowdcast:http_error_ratio:rate5m_total or vector(0)", "all instances")], "percentunit", w=12, h=8)
    p4 = b.series("WebSocket connections by instance", [("A", "ws_connections_active", "{{instance}}")], "short", w=12, h=8)
    b.place_row([(p3, (12, 8)), (p4, (12, 8))])
    return b.build(["overview"], chaos_annotations())


def load_balancer():
    b = Builder("crowdcast-lb", "Load Balancer & Distribution",
                "Actual traffic share per instance against the 50/30/20 target weights.")
    b.row("Distribution")
    share = b.series("Traffic share vs target", [
        ("A", "crowdcast:traffic_share:percent", "{{instance}} actual"),
        ("B", "vector(50)", "target crowdcast-api-01 (50%)"),
        ("C", "vector(30)", "target crowdcast-api-02 (30%)"),
        ("D", "vector(20)", "target crowdcast-api-03 (20%)"),
    ], "percent", w=16, h=9, description="Target lines are constants from nginx weights 5:3:2.")
    pie = b.series("Share right now", [("A", "crowdcast:traffic_share:percent", "{{instance}}")],
                   "percent", w=8, h=9, kind="bargauge")
    b.place_row([(share, (16, 9)), (pie, (8, 9))])
    b.row("Edge and upstreams")
    edge = b.series("nginx requests / s", [("A", "sum(rate(nginx_http_requests_total[1m]))", "nginx total")], "reqps", w=12, h=8)
    per = b.series("Response time by instance (p50 / p95)", [
        ("A", "crowdcast:http_latency:p50_5m", "{{instance}} p50"),
        ("B", "crowdcast:http_latency:p95_5m", "{{instance}} p95"),
    ], "s", w=12, h=8, description="nginx does not export per-upstream timings; the app's own request latency is shown instead.")
    b.place_row([(edge, (12, 8)), (per, (12, 8))])
    return b.build(["lb"])


def node_health():
    b = Builder("crowdcast-nodes", "Node Health", "CPU, memory, runtime and connections per backend instance.")
    b.row("Up / down")
    b.stats([
        ("Nodes up", 'sum(up{job="crowdcast-api"})', "short", "#73BF69"),
        ("Nodes expected", 'count(up{job="crowdcast-api"})', "short", "#5794F2"),
    ])
    b.row("Resources")
    cpu = b.series("CPU % (app process)", [("A", "node_cpu_percent", "{{instance}}")], "percent", w=12, h=8)
    mem = b.series("Memory % of container limit", [("A", "node_memory_percent", "{{instance}}")], "percent", w=12, h=8)
    b.place_row([(cpu, (12, 8)), (mem, (12, 8))])
    cad = b.series("Container CPU (cAdvisor, cores)", [
        ("A", 'sum by (name) (rate(container_cpu_usage_seconds_total{name=~"crowdcast-api-.*"}[1m]))', "{{name}}"),
    ], "short", w=12, h=8)
    cadm = b.series("Container memory (cAdvisor)", [
        ("A", 'sum by (name) (container_memory_working_set_bytes{name=~"crowdcast-api-.*"})', "{{name}}"),
    ], "bytes", w=12, h=8)
    b.place_row([(cad, (12, 8)), (cadm, (12, 8))])
    b.row("Runtime and connections")
    gor = b.series("Goroutines", [("A", 'go_goroutines{job="crowdcast-api"}', "{{instance}}")], "short", w=8, h=8)
    gc = b.series("GC cycles / s", [("A", 'sum by (instance) (rate(go_gc_duration_seconds_count{job="crowdcast-api"}[1m]))', "{{instance}}")], "short", w=8, h=8)
    ws = b.series("WebSocket connections", [("A", "ws_connections_active", "{{instance}}")], "short", w=8, h=8)
    b.place_row([(gor, (8, 8)), (gc, (8, 8)), (ws, (8, 8))])
    return b.build(["nodes"])


def data_stores():
    b = Builder("crowdcast-data", "Redis & Postgres", "Operations, latency, memory and connections for the data stores.")
    b.row("Redis")
    ops = b.series("Redis commands / s", [("A", "sum(rate(redis_commands_processed_total[1m]))", "commands")], "ops", w=8, h=8)
    lat = b.series("Redis average command latency", [
        ("A", "sum(rate(redis_commands_duration_seconds_total[1m])) / sum(rate(redis_commands_total[1m]))", "avg"),
        ("B", "histogram_quantile(0.95, sum by (le, op) (rate(redis_op_duration_seconds_bucket[5m])))", "app p95 {{op}}"),
    ], "s", w=8, h=8)
    mem = b.series("Redis memory and clients", [
        ("A", "redis_memory_used_bytes", "memory"),
        ("B", "redis_connected_clients", "clients"),
    ], "bytes", w=8, h=8)
    b.place_row([(ops, (8, 8)), (lat, (8, 8)), (mem, (8, 8))])
    b.row("Postgres")
    conns = b.series("Connections", [("A", "pg_stat_database_numbackends", "{{datname}}")], "short", w=8, h=8)
    tx = b.series("Transactions / s", [("A", 'sum by (datname) (rate(pg_stat_database_xact_commit{datname!=""}[1m]))', "{{datname}} commits")], "ops", w=8, h=8)
    size = b.series("Database size", [("A", 'pg_database_size_bytes{datname="crowdcast"}', "crowdcast")], "bytes", w=8, h=8)
    b.place_row([(conns, (8, 8)), (tx, (8, 8)), (size, (8, 8))])
    return b.build(["data"])


def failure_drill():
    b = Builder("crowdcast-failure", "Failure Drill",
                "Watch error rate and distribution while a node is failed. Orange markers are chaos commands.")
    b.row("During the drill")
    b.stats([
        ("Error rate", "100 * (crowdcast:http_error_ratio:rate5m_total or vector(0))", "percent", "#FF9830"),
        ("Nodes up", 'sum(up{job="crowdcast-api"})', "short", "#73BF69"),
        ("Chaos events (5m)", "sum(increase(chaos_events_total[5m])) or vector(0)", "short", "#FADE2A"),
    ])
    d = b.series("Traffic distribution", [("A", "crowdcast:traffic_share:percent", "{{instance}}")], "percent", w=12, h=9)
    e = b.series("5xx ratio", [("A", "crowdcast:http_error_ratio:rate5m or vector(0)", "{{instance}}")], "percentunit", w=12, h=9)
    b.place_row([(d, (12, 9)), (e, (12, 9))])
    u = b.series("Node up/down", [("A", 'up{job="crowdcast-api"}', "{{node}}")], "short", w=24, h=7,
                 description="1 = scraped, 0 = NodeDown is firing.")
    b.place_row([(u, (24, 7))])
    return b.build(["drill"], chaos_annotations())


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    for dash in [overview(), load_balancer(), node_health(), data_stores(), failure_drill()]:
        path = OUT / f"{dash['uid']}.json"
        path.write_text(json.dumps(dash, indent=2, ensure_ascii=False) + "\n")
        print("wrote", path.relative_to(OUT.parents[2]))


if __name__ == "__main__":
    main()
