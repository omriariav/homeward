#!/usr/bin/env python3
"""Dependency-free Mac CLI for an experimental XCTest Home Screen runner."""
import argparse
import datetime
import json
import os
from pathlib import Path
import plistlib
import subprocess
import sys

ROOT = Path(__file__).resolve().parent
BUILD = ROOT / '.build'
PROJECT = ROOT / 'runner/HomeScreenPOC.xcodeproj'


def environment():
    env = os.environ.copy()
    env.setdefault('DEVELOPER_DIR', '/Applications/Xcode.app/Contents/Developer')
    return env


def execute(args, **kwargs):
    return subprocess.run([str(a) for a in args], env=environment(), check=True, **kwargs)


def normalized(value):
    number = float(value)
    if not 0 <= number <= 1:
        raise argparse.ArgumentTypeError('coordinate must be between 0 and 1')
    return number


def duration(value):
    number = float(value)
    if not 0 <= number <= 10:
        raise argparse.ArgumentTypeError('duration must be between 0 and 10 seconds')
    return number


def require_unlocked(response):
    if response.get('info', {}).get('outcome') != 'success':
        raise ValueError('Could not verify device lock state; refusing to send a gesture.')
    if response.get('result', {}).get('passcodeRequired') is not False:
        raise ValueError('Unlock the iPhone before running gestures. Keep it awake during XCTest waits.')


def configure_run(data, command):
    """Set runner environment in modern or legacy xctestrun files."""
    count = 0
    def walk(node):
        nonlocal count
        if isinstance(node, dict):
            if 'TestBundlePath' in node:
                node.setdefault('EnvironmentVariables', {})['HSC_COMMAND'] = json.dumps(command)
                count += 1
            else:
                for value in node.values():
                    walk(value)
        elif isinstance(node, list):
            for value in node:
                walk(value)
    walk(data)
    if count != 1:
        raise ValueError(f'Expected one test runner, found {count}')
    return data


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest='command', required=True)
    preview = sub.add_parser('preview', help='Review proposed pages, Dock, moves and exclusions without changing the phone')
    preview.add_argument('draft', type=Path)
    verify = sub.add_parser('verify-layout', help='Require exact match between selected layout and device readback')
    verify.add_argument('--expected', type=Path, required=True)
    verify.add_argument('--actual', type=Path, required=True)
    plan = sub.add_parser('plan', help='Draft a layout using a headless LLM; never modifies the phone')
    source = plan.add_mutually_exclusive_group(required=True)
    source.add_argument('--layout', type=Path, help='Current cfgutil JSON export')
    source.add_argument('--interview', type=Path, help='Completed interview with saved layout and answers')
    plan.add_argument('--metadata', type=Path, help='Optional app labels/colors and widget observations')
    plan.add_argument('--request', help='User context, or additional instructions after an interview')
    plan.add_argument('--provider', choices=('claude', 'codex'))
    plan.add_argument('--model', help='Override the provider-specific model')
    plan.add_argument('--effort', choices=('low', 'medium', 'high'))
    plan.add_argument('--style', choices=('purpose', 'color'))
    plan.add_argument('--selection', choices=('preserve', 'curate'), default='preserve', help='Curate selects relevant apps and leaves others off Home Screen')
    plan.add_argument('--max-pages', type=int, choices=range(1, 16), help='Hard Home Screen page limit, excluding Dock')
    plan.add_argument('--keep', action='append', default=[], help='Inventory item ID that must remain visible')
    plan.add_argument('--allow-hide', action='append', default=[], help='Explicit input item ID allowed to leave Home Screen')
    plan.add_argument('--timeout', type=int, default=180)
    plan.add_argument('--output', type=Path, required=True, help='New draft JSON path (never overwritten)')
    plan.add_argument('--dry-run', action='store_true', help='Save normalized input without calling an LLM')
    interview = sub.add_parser('interview', help='Ask focused questions based on user context and the existing layout')
    origin = interview.add_mutually_exclusive_group(required=True)
    origin.add_argument('--layout', type=Path, help='Saved cfgutil layout for a new interview')
    origin.add_argument('--resume', type=Path, help='Resume a saved interview without regenerating questions')
    interview.add_argument('--metadata', type=Path)
    interview.add_argument('--request', help='Free-text context; prompted if omitted in interactive mode')
    interview.add_argument('--provider', choices=('claude', 'codex'))
    interview.add_argument('--model')
    interview.add_argument('--effort', choices=('low', 'medium', 'high'))
    interview.add_argument('--style', choices=('purpose', 'color'))
    interview.add_argument('--max-questions', type=int, choices=range(1, 7), default=4)
    interview.add_argument('--timeout', type=int, default=180)
    interview.add_argument('--output', type=Path, help='New interview file; answers are saved after each response')
    interview.add_argument('--questions-only', action='store_true', help='Generate/save questions for an external UI without reading stdin')
    interview.add_argument('--answers', type=Path, help='JSON mapping question IDs to answer text or null to skip')
    for ai_command in (plan, interview):
        consent = ai_command.add_mutually_exclusive_group()
        consent.add_argument('--consent-ai', action='store_true', help='Authorize this request to send supplied layout/context/answers to the selected AI service')
        consent.add_argument('--no-ai', action='store_true', help='Decline external AI requests; local dry runs and saved-answer editing remain available')
    sub.add_parser('doctor', help='Check Xcode and signing identities')
    sub.add_parser('devices', help='List connected devices')
    shot = sub.add_parser('screenshot', help='Capture screen without installing the runner')
    shot.add_argument('--device', required=True)
    shot.add_argument('--output', type=Path, required=True)
    build = sub.add_parser('build', help='Build the XCTest runner')
    build.add_argument('--team', help='Apple Developer team ID (required for device builds)')
    build.add_argument('--device', help='Build for this physical device UDID (recommended for provisioning)')
    build.add_argument('--bundle-id', default='local.homescreen.poc')
    build.add_argument('--simulator', action='store_true', help='Unsigned simulator build for compile validation')
    build.add_argument('--allow-provisioning', action='store_true', help='Allow Xcode to update signing profiles')
    for action in ('inspect', 'home', 'tap', 'hold', 'drag', 'type', 'remove-icon', 'next-pages',
                   'widget-gallery', 'tap-button', 'move-icon', 'remove-widget', 'hide-other-pages', 'remove-hidden-pages'):
        cmd = sub.add_parser(action, help=f'Run {action} through XCTest (requires signed build)')
        cmd.add_argument('--device', required=True)
        cmd.add_argument('--simulator', action='store_true', help='Use simulator build and simulator UDID')
        if action not in ('home', 'remove-icon', 'widget-gallery', 'move-icon', 'remove-widget', 'hide-other-pages', 'remove-hidden-pages'):
            cmd.add_argument('--app', help='Bundle ID of the system UI to inspect or interact with')
        if action in ('hide-other-pages', 'remove-hidden-pages'):
            cmd.add_argument('--keep-pages', type=int, choices=range(1, 16), default=1, help='Keep this many leading Home Screen pages')
        if action == 'widget-gallery':
            cmd.add_argument('--first-page', action='store_true', help='Open the gallery on Home page 1 instead of the final empty page')
        if action == 'type':
            cmd.add_argument('text', help='Text to enter in the sole visible SpringBoard search field')
        if action == 'move-icon':
            cmd.add_argument('name', help='Exact visible icon name on Home page 1')
            cmd.add_argument('to_x', type=normalized)
            cmd.add_argument('to_y', type=normalized)
            cmd.add_argument('--round-trip', action='store_true', help='Verify the move, then restore original icon positions')
        if action == 'remove-widget':
            cmd.add_argument('name', help='Exact widget name on the current page; requires a unique visible widget')
        if action == 'remove-icon':
            cmd.add_argument('--page', type=int, choices=range(1, 13), default=1)
            cmd.add_argument('name', help='Exact name of the icon on Home page 1; keeps the app installed')
        if action == 'tap-button':
            cmd.add_argument('name', help='Exact visible button label or identifier')
        if action == 'next-pages':
            cmd.add_argument('--count', type=int, choices=range(1, 13), default=1)
        if action in ('tap', 'hold', 'drag'):
            cmd.add_argument('x', type=normalized)
            cmd.add_argument('y', type=normalized)
        if action in ('hold', 'drag'):
            cmd.add_argument('--duration', type=duration, default=1.2, help='Initial hold in seconds')
        if action == 'drag':
            cmd.add_argument('to_x', type=normalized)
            cmd.add_argument('to_y', type=normalized)
            cmd.add_argument('--end-hold', type=duration, default=0.5)
    return p


def main():
    args = parser().parse_args()
    if args.command == 'preview':
        import preview
        preview.run(args.draft)
    elif args.command == 'verify-layout':
        from planner import verify_layout
        verify_layout(json.loads(args.expected.read_text()), json.loads(args.actual.read_text()))
        print('Verified: device readback exactly matches the selected layout.')
    elif args.command == 'plan':
        if args.timeout < 1:
            raise ValueError('--timeout must be positive')
        if args.output.exists():
            raise ValueError('--output already exists; choose a new draft path')
        import planner
        planner.run(args)
    elif args.command == 'interview':
        import interview
        interview.run(args)
    elif args.command == 'doctor':
        execute(['xcodebuild', '-version'])
        execute(['security', 'find-identity', '-v', '-p', 'codesigning'])
        print('Device setup: trust this Mac, enable Developer Mode and Settings > Developer > Enable UI Automation.')
        print('A valid Apple Development signing identity and provisioning profile are needed for the runner.')
    elif args.command == 'devices':
        execute(['xcrun', 'devicectl', 'list', 'devices', '--timeout', '20'])
    elif args.command == 'screenshot':
        if args.output.suffix.lower() != '.png':
            raise ValueError('--output must end in .png')
        args.output.parent.mkdir(parents=True, exist_ok=True)
        execute(['xcrun', 'devicectl', 'device', 'capture', 'screenshot', '--device', args.device,
                 '--destination', args.output.resolve(), '--timeout', '20'])
    elif args.command == 'build':
        if args.simulator and args.device:
            raise ValueError('--device cannot be combined with --simulator during build')
        if not args.simulator and not args.team:
            raise ValueError('Device builds require --team YOUR_TEAM_ID. Run doctor and configure signing in Xcode.')
        target = 'simulator' if args.simulator else 'device'
        command = ['xcodebuild', 'build-for-testing', '-project', PROJECT, '-scheme', 'HomeScreenPOC',
                   '-derivedDataPath', BUILD / target, '-destination',
                   ('generic/platform=iOS Simulator' if args.simulator else
                    f'id={args.device}' if args.device else 'generic/platform=iOS'),
                   f'PRODUCT_BUNDLE_IDENTIFIER={args.bundle_id}']
        command += ['CODE_SIGNING_ALLOWED=NO'] if args.simulator else [f'DEVELOPMENT_TEAM={args.team}']
        if args.allow_provisioning:
            command.append('-allowProvisioningUpdates')
            if args.device:
                command.append('-allowProvisioningDeviceRegistration')
        execute(command)
    else:
        if not args.simulator and args.command != 'inspect':
            state = execute(['xcrun', 'devicectl', 'device', 'info', 'lockState',
                             '--device', args.device, '--json-output', '-', '--timeout', '15'],
                            capture_output=True, text=True)
            require_unlocked(json.loads(state.stdout))
        platform = 'simulator' if args.simulator else 'device'
        products = BUILD / platform / 'Build/Products'
        candidates = list(products.glob('HomeScreenPOC_*.xctestrun'))
        if len(candidates) != 1:
            raise ValueError(f'Build the {platform} runner first; expected one .xctestrun in {products}.')
        command = {'action': args.command}
        for source, key in [('x', 'x'), ('y', 'y'), ('to_x', 'toX'), ('to_y', 'toY'),
                            ('duration', 'duration'), ('end_hold', 'endHold'), ('text', 'text'), ('name', 'name'),
                            ('app', 'appBundle'), ('count', 'count'), ('round_trip', 'roundTrip'), ('first_page', 'firstPage'), ('page', 'page'), ('keep_pages', 'keepPages')]:
            if hasattr(args, source):
                command[key] = getattr(args, source)
        # Keep alongside the original so __TESTROOT__ retains its meaning.
        stamp = datetime.datetime.now().strftime('%Y%m%d-%H%M%S-%f')
        run = products / f'command-{stamp}.xctestrun'
        with candidates[0].open('rb') as f:
            data = configure_run(plistlib.load(f), command)
        with run.open('wb') as f:
            plistlib.dump(data, f)
        results = ROOT / 'artifacts' / f'{stamp}-{args.command}.xcresult'
        results.parent.mkdir(exist_ok=True)
        print(f'Command: {json.dumps(command)}\nEvidence: {results}', flush=True)
        try:
            execute(['xcodebuild', 'test-without-building', '-xctestrun', run,
                     '-destination', f'id={args.device}', '-resultBundlePath', results,
                     '-parallel-testing-enabled', 'NO', '-maximum-concurrent-test-device-destinations', '1'])
        finally:
            run.unlink(missing_ok=True)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(f'Error: {error}', file=sys.stderr)
        sys.exit(1)
