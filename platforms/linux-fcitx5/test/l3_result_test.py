import unittest

from l3_result import assess, audit_report


class ResultTests(unittest.TestCase):
    def result(self, **changes):
        args = dict(elapsed_seconds=96 * 3600, requested_hours=96, interrupted=False,
                    releases={'B': {'name': 'b', 'active_h': 6}, 'F': {'name': 'f', 'failed_h': 27},
                              'G': {'name': 'g', 'active_h': 52}}, typing=[{'elapsed_seconds': h * 3600, 'ok': True} for h in range(0, 96, 2)], error='', initial_typing=True,
                    coverage={n: 'pass' for n in ('sleep_wake', 'login', 'usability', 'restoration')})
        args.update(changes)
        return assess(**args)

    def test_complete_evidence_passes(self):
        self.assertTrue(self.result()['passed'])

    def test_short_scenario_has_no_fixed_duration_floor(self):
        result = self.result(elapsed_seconds=3600, requested_hours=1,
                             releases={'B': {'name': 'b', 'active_h': .1},
                                       'F': {'name': 'f', 'failed_h': .3},
                                       'G': {'name': 'g', 'active_h': .5}},
                             typing=[{'elapsed_seconds': 0, 'ok': True}], coverage={})
        self.assertTrue(result['passed'])
        self.assertFalse(result['full_journey_passed'])

    def test_invalid_or_missing_duration_does_not_pass(self):
        for hours in (None, 0, -1, float('nan'), float('inf')):
            with self.subTest(hours=hours):
                self.assertFalse(self.result(requested_hours=hours)['passed'])

    def test_empty_and_interrupted_runs_cannot_pass(self):
        for changes in ({'releases': {}}, {'interrupted': True}, {'elapsed_seconds': 93.22 * 3600},
                        {'elapsed_seconds': 2 * 3600, 'requested_hours': 2}, {'error': 'failed'}):
            with self.subTest(changes=changes):
                self.assertFalse(self.result(**changes)['scheduler_passed'])

    def test_typing_failure_or_missing_samples_fails(self):
        for typing in ([], [True], [True, False, True],
                       [{'elapsed_seconds': 0, 'ok': True}] * 48,
                       [{'elapsed_seconds': h * 3600, 'ok': h != 6} for h in range(0, 96, 2)]):
            self.assertFalse(self.result(typing=typing)['passed'])

    def test_missing_release_or_fault_activated_fails(self):
        self.assertFalse(self.result(releases={'B': {'name': 'b', 'active_h': 6}})['passed'])
        self.assertFalse(self.result(releases={'B': {'name': 'b', 'active_h': 6},
                                              'F': {'name': 'f', 'failed_h': 27, 'active_h': 26},
                                              'G': {'name': 'g', 'active_h': 52}})['passed'])

    def test_initial_failure_and_unrecorded_duration_fail(self):
        self.assertFalse(self.result(initial_typing=False)['passed'])
        report = {'passed': True, 'releases': {'B': {'name': 'b', 'active_h': 6},
                                             'F': {'name': 'f', 'failed_h': 27},
                                             'G': {'name': 'g', 'active_h': 52}},
                  'samples': [{'h': 95.971}], 'timeline': [{'kind': 'typed_ok', 'ok': True}]}
        audit = audit_report(report)
        self.assertFalse(audit['passed'])
        self.assertFalse(audit['legacy_summary']['duration_recorded'])
        self.assertIsNone(audit['elapsed_seconds'])
        self.assertEqual(audit['legacy_summary']['last_observed_hours'], 95.971)

    def test_long_missing_section_of_typing_is_not_continuous(self):
        samples = [{'elapsed_seconds': h * 3600, 'ok': True} for h in range(0, 96, 2)]
        samples[20:30] = [{'elapsed_seconds': 38 * 3600, 'ok': True}] * 10
        self.assertFalse(self.result(typing=samples)['passed'])

    def test_scheduler_completion_is_not_complete_user_journey(self):
        result = self.result(coverage={})
        self.assertTrue(result['scheduler_passed'])
        self.assertTrue(result['passed'])
        self.assertFalse(result['full_journey_passed'])
        self.assertFalse(self.result(coverage={}, required_journeys=('login',))['passed'])


if __name__ == '__main__':
    unittest.main()
