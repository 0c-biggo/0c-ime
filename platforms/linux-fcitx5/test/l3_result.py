"""Evaluate recorded L3 evidence without performing updates or desktop operations."""
import math


def continuous_typing(typing, elapsed_seconds, requested_hours):
    if type(requested_hours) not in (int, float) or not math.isfinite(requested_hours) or requested_hours <= 0:
        return False
    if type(elapsed_seconds) not in (int, float) or not math.isfinite(elapsed_seconds) or elapsed_seconds < 0:
        return False
    if not isinstance(typing, list) or len(typing) < math.ceil(requested_hours / 2):
        return False
    times = []
    for sample in typing:
        if not isinstance(sample, dict) or sample.get('ok') is not True:
            return False
        value = sample.get('elapsed_seconds')
        if not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0:
            return False
        times.append(value)
    # The loop samples every 5 minutes and types every 2 hours. Missing a long
    # section of the run must not pass merely because enough old samples exist.
    slack, interval = 600, 2 * 3600
    return (times == sorted(times) and times[0] <= slack and times[-1] <= elapsed_seconds
            and elapsed_seconds - times[-1] <= interval + slack
            and all(b - a <= interval + slack for a, b in zip(times, times[1:])))


def assess(elapsed_seconds, requested_hours, interrupted, releases, typing, error, coverage, initial_typing=False,
           required_journeys=()):
    names = {r.get('name'): r for r in releases.values()}
    checks = {
        'requested_duration': type(elapsed_seconds) in (int, float) and type(requested_hours) in (int, float)
                           and math.isfinite(elapsed_seconds) and math.isfinite(requested_hours)
                           and requested_hours > 0 and elapsed_seconds >= requested_hours * 3600,
        'not_interrupted': not interrupted and not error,
        'all_releases': set(names) == {'b', 'f', 'g'} and len(releases) == 3,
        'healthy_releases_active': all('active_h' in names.get(n, {}) for n in ('b', 'g')),
        'fault_rejected': 'failed_h' in names.get('f', {}) and 'active_h' not in names.get('f', {}),
        'initial_typing': initial_typing is True,
        'continuous_typing': continuous_typing(typing, elapsed_seconds, requested_hours),
    }
    scheduler_passed = all(checks.values())
    journey = {name: coverage.get(name) == 'pass' for name in ('sleep_wake', 'login', 'usability', 'restoration')}
    return {'checks': checks, 'journey_checks': journey, 'scheduler_passed': scheduler_passed,
            'required_journeys': list(required_journeys),
            'passed': scheduler_passed and all(journey.get(name, False) for name in required_journeys),
            'full_journey_passed': scheduler_passed and all(journey.values()),
            'elapsed_seconds': elapsed_seconds,
            'elapsed_hours': round(elapsed_seconds / 3600, 3) if type(elapsed_seconds) in (int, float) and math.isfinite(elapsed_seconds) else None,
            'requested_hours': requested_hours}


def audit_report(report):
    # Legacy reports have only sample time lower bounds. Never infer the final
    # runtime or stop reason from a passed flag, wall clock or scheduled end date.
    elapsed = report.get('elapsed_seconds')
    duration_recorded = isinstance(elapsed, (float, int)) and math.isfinite(elapsed) and elapsed >= 0
    result = assess(elapsed if duration_recorded else None, report.get('requested_hours'),
                    report.get('stop_reason') != 'duration_reached', report.get('releases', {}),
                    report.get('typing', []), report.get('error', ''), report.get('coverage', {}),
                    report.get('initial_typing', False),
                    report.get('required_journeys', ('sleep_wake', 'login', 'usability', 'restoration')))
    typed = [entry for entry in report.get('timeline', []) if entry.get('kind') == 'typed_ok']
    result['legacy_summary'] = {'original_passed': report.get('passed'), 'duration_recorded': duration_recorded,
                              'sample_count': len(report.get('samples', [])), 'typing_count': len(typed),
                              'typing_all_correct': bool(typed) and all(e.get('ok') is True for e in typed),
                              'last_observed_hours': (report.get('samples') or [{}])[-1].get('h')}
    return result


if __name__ == '__main__':
    import argparse
    import json
    from pathlib import Path
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--audit', type=Path, required=True, help='read-only report re-evaluation')
    args = parser.parse_args()
    result = audit_report(json.loads(args.audit.read_text()))
    print(json.dumps(result, ensure_ascii=False, indent=2))
    raise SystemExit(0 if result['passed'] else 1)
