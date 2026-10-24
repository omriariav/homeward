import argparse
import unittest
from homescreen import configure_run, normalized, duration, require_unlocked


class CLITests(unittest.TestCase):
    def test_lock_guard_fails_closed(self):
        for response in ({}, {'info': {'outcome': 'success'}, 'result': {}},
                         {'info': {'outcome': 'success'}, 'result': {'passcodeRequired': True}}):
            with self.assertRaises(ValueError):
                require_unlocked(response)
        require_unlocked({'info': {'outcome': 'success'}, 'result': {'passcodeRequired': False}})

    def test_invalid_coordinates_cannot_reach_device(self):
        for value in ('-0.1', '1.1', 'nan', 'inf'):
            with self.assertRaises(argparse.ArgumentTypeError):
                normalized(value)

    def test_invalid_hold_duration(self):
        for value in ('-1', '11', 'nan'):
            with self.assertRaises(argparse.ArgumentTypeError):
                duration(value)

    def test_modern_runner_preserves_paths_and_environment(self):
        target = {'TestBundlePath': '__TESTROOT__/runner.xctest',
                  'EnvironmentVariables': {'EXISTING': 'yes'}}
        data = {'TestConfigurations': [{'TestTargets': [target]}]}
        configure_run(data, {'action': 'inspect'})
        self.assertEqual(target['TestBundlePath'], '__TESTROOT__/runner.xctest')
        self.assertEqual(target['EnvironmentVariables']['EXISTING'], 'yes')
        self.assertIn('inspect', target['EnvironmentVariables']['HSC_COMMAND'])

    def test_rejects_ambiguous_targets(self):
        with self.assertRaises(ValueError):
            configure_run({'a': {'TestBundlePath': 'a'}, 'b': {'TestBundlePath': 'b'}}, {})


if __name__ == '__main__':
    unittest.main()
