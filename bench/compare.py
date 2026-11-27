#!/usr/bin/env python3
"""results/<RUN_ID>/ 의 k6 결과를 시나리오별로 대상(게이트웨이)끼리 나란히 보여 준다.
사용: ./compare.py <RUN_ID>   (예: ./compare.py 20261127-1530)
"""
import csv
import glob
import json
import os
import re
import sys

TARGET_ORDER = ["gateway", "litellm1", "litellm4"]


def mem_mib(text):
    """'123.4MiB / 3GiB' 의 앞쪽 값을 MiB 로"""
    m = re.match(r"\s*([\d.]+)\s*([KMG]i?B)", text)
    if not m:
        return 0.0
    value, unit = float(m.group(1)), m.group(2)
    return value * {"KiB": 1 / 1024, "KB": 1 / 1024, "MiB": 1, "MB": 1, "GiB": 1024, "GB": 1024}[unit]


def resource_peaks(path):
    cpu, mem = 0.0, 0.0
    if not os.path.exists(path):
        return None, None
    with open(path) as f:
        for row in csv.reader(f):
            if len(row) < 3:
                continue
            try:
                cpu = max(cpu, float(row[1].rstrip("%")))
            except ValueError:
                continue
            mem = max(mem, mem_mib(row[2]))
    return cpu, mem


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    run_dir = os.path.join(os.path.dirname(os.path.abspath(__file__)), "results", sys.argv[1])
    results = {}
    for path in glob.glob(os.path.join(run_dir, "*.json")):
        name = os.path.basename(path)[:-5]
        target, _, scenario = name.partition("-")
        with open(path) as f:
            results.setdefault(scenario, {})[target] = (json.load(f), path[:-5] + ".stats.csv")
    if not results:
        sys.exit(f"결과가 없다: {run_dir}")

    def order(s):
        return (s.startswith("stream"), s.startswith("vus"), int(re.sub(r"\D", "", s) or 0), s)

    for scenario in sorted(results, key=order):
        print(f"\n### {scenario}")
        print("| 대상 | 처리량(/s) | p50(ms) | p95(ms) | p99(ms) | 첫 바이트 p50(ms) | 실패 | 밀린 요청 | CPU 최대(%) | 메모리 최대(MiB) |")
        print("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
        for target in TARGET_ORDER:
            if target not in results[scenario]:
                continue
            r, stats = results[scenario][target]
            d, w = r["duration_ms"], r["ttfb_ms"]
            cpu, mem = resource_peaks(stats)
            dropped = r["dropped"]["count"] if r["dropped"] else 0
            print(
                f"| {target} | {r['requests']['rate']:.1f} | {d['med']:.1f} | {d['p(95)']:.1f} | {d['p(99)']:.1f} | "
                f"{w['med']:.1f} | {r['failed_rate']['rate'] * 100:.2f}% | {dropped} | "
                f"{'-' if cpu is None else f'{cpu:.0f}'} | {'-' if mem is None else f'{mem:.0f}'} |"
            )


if __name__ == "__main__":
    main()
