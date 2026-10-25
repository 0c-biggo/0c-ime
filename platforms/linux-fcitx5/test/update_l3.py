#!/usr/bin/env python3
"""Optional scheduler observation with an explicit duration and release schedule on a dedicated account.

The account installs A like a user and keeps a desktop (Xvfb, Fcitx5, dunst) up. The updater runs with the
formal parameters (timer every 15 minutes, feed check every 6 hours + jitter, real 5-minute idle); only the
feed URL points at the local HTTPS test service. B (core), F (fault) and G (fix) publication times must
be specified for the scenario; there is no fixed multi-day requirement. Every 5 minutes the engine version, update stage and notices
are sampled; every 2 hours a sentence is typed. The report records how long each release took from
publication to discovery and to activation, and whether the running engine ever regressed.
Run it detached (systemd-run --user); it writes report.json when it ends or is stopped.
Passing covers the recorded scheduler scenario, not sleep, login or human usability.
"""
import argparse
import json
import math
import os
from pathlib import Path
import signal
import time

import update_e2e as e2e
from l3_result import assess

def main():
    p = argparse.ArgumentParser(description=__doc__)
    for name in 'abfg':
        p.add_argument('--' + name, type=Path, required=True)
    p.add_argument('--tools', type=Path, required=True)
    p.add_argument('--hours', type=float, required=True, help='scenario observation duration, no fixed minimum')
    p.add_argument('--release-hours', type=float, nargs=3, required=True, metavar=('B', 'F', 'G'),
                   help='hours after start to publish B, F and G, in increasing order')
    p.add_argument('--work', type=Path, default=e2e.HOME / 'l3/work')
    p.add_argument('--result', type=Path, default=e2e.HOME / 'l3/results' / time.strftime('%Y%m%d-%H%M%S'))
    args = p.parse_args()
    if not math.isfinite(args.hours) or args.hours <= 0:
        p.error('--hours must be finite and positive')
    if (any(not math.isfinite(h) or h < 0 or h >= args.hours for h in args.release_hours)
            or not args.release_hours[0] < args.release_hours[1] < args.release_hours[2]):
        p.error('--release-hours must be finite, nonnegative, strictly increasing and before --hours')
    args.tools = args.tools.resolve()
    args.case, args.updater, args.f_alt = 'l3', None, None
    case = e2e.Case(args)
    case.report['scope'] = 'scheduler_observation'
    case.report['release_hours'] = args.release_hours
    case.report['timing'] = {'timer': '15m (formal)', 'feed_check': '6h + 0-30m jitter (formal)', 'idle_needed': '5m (real)'}
    stop = []
    signal.signal(signal.SIGTERM, lambda *_: stop.append(1))
    releases = {}
    typing = []
    start = None
    initial_typing = False
    case.report['coverage'] = {name: 'not-run' for name in ('sleep_wake', 'login', 'usability', 'restoration')}
    try:
        case.reset()
        root = case.unpack(args.a.resolve())
        e2e.run(root / 'bin/0c-update', 'install', '-bundle', root, '-channel', 'internal-preview')
        case.unit('zc-e2e-feed', args.tools / '0c-e2e-feed', '-addr', f'127.0.0.1:{e2e.PORT}', '-root', case.work / 'feed',
                  '-cert', args.tools / 'feed-server.pem', '-key', args.tools / 'feed-server.key')
        a_feed = case.publish(args.a.resolve(), 1)
        e2e.run(e2e.STABLE, 'schedule', 'install', '-feed', e2e.FEED)
        case.start_desktop()
        initial_typing = case.type('su3cl3 Return', '你好')
        case.check(initial_typing, 'a_typing')
        start = time.monotonic()
        serial, pending, last_type, samples = 1, list(zip(args.release_hours, 'bfg', ['core'] * 3)), 0.0, []
        while time.monotonic() - start < args.hours * 3600 and not stop:
            elapsed = (time.monotonic() - start) / 3600
            if pending and elapsed >= pending[0][0]:
                _, name, kind = pending.pop(0)
                serial += 1
                feed = case.publish(getattr(args, name).resolve(), serial, kind)
                releases[feed['version']] = {'name': name, 'published_h': round(elapsed, 3)}
            h = e2e.health() or {}
            s = e2e.state() or {}
            sample = {'h': round(elapsed, 3), 'engine': h.get('version'), 'stage': s.get('stage'),
                      'blocked': (s.get('blocked') or {}).get('reason'), 'target': (s.get('target') or {}).get('version')}
            samples.append(sample)
            for version, r in releases.items():
                if sample['target'] == version and 'discovered_h' not in r:
                    r['discovered_h'] = sample['h']
                if sample['engine'] == version and 'active_h' not in r:
                    r['active_h'] = sample['h']
                if version in (s.get('failed') or []) and 'failed_h' not in r:
                    r['failed_h'] = sample['h']
            if time.monotonic() - last_type > 2 * 3600:
                last_type = time.monotonic()
                ok = case.type('su3cl3 Return', '你好')
                typing.append({'elapsed_seconds': time.monotonic() - start, 'ok': ok})
                case.log('typed_ok', ok=ok, engine=h.get('version'))
            case.report['samples'], case.report['releases'] = samples[-2000:], releases
            (case.result / 'progress.json').write_text(json.dumps({'releases': releases, 'last': sample}, ensure_ascii=False, indent=2))
            time.sleep(300)
        case.report['notifications'] = case.notices()
    except Exception as error:
        case.report['error'] = str(error)
    finally:
        elapsed = time.monotonic() - start if start is not None else 0
        case.report.update(assess(elapsed, args.hours, bool(stop), releases, typing,
                                 case.report['error'], case.report['coverage'], initial_typing))
        case.report['initial_typing'], case.report['typing'] = initial_typing, typing
        case.report['stop_reason'] = 'interrupted' if stop else 'error' if case.report['error'] else 'duration_reached'
        case.finish()
    print(case.result)
    raise SystemExit(0 if case.report['passed'] else 1)


if __name__ == '__main__':
    main()
