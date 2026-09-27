#!/usr/bin/env python3
"""Synthetic campaign stacking on isolated Stage, using the guarded ops adapter.

Requires accept-benefit-campaign-stage.py and accept-subscription-v2-stage.py
from the ops repository. No real payment or external model request is sent.
--hold-ui retains disposable fixtures until ui-done exists (at most 20 minutes).
"""
import argparse
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import time
from decimal import Decimal, ROUND_HALF_UP


def main():
    bootstrap = argparse.ArgumentParser(add_help=False)
    bootstrap.add_argument('--stage-adapter', required=True, type=Path)
    known, remaining = bootstrap.parse_known_args()
    spec = importlib.util.spec_from_file_location('benefit_stage', known.stage_adapter)
    benefit = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(benefit)
    stage = benefit.stage
    parser = stage.args_parser()
    parser.add_argument('--hold-ui', action='store_true')
    args = parser.parse_args(remaining)
    module = stage.load_source(args.source_root)

    class Fixture(benefit.fixture_type(module)):
        def gift_user(self, label, paid=False):
            user = self.eligible_user(label, paid)
            campaign = self.make_campaign(label)
            self.activate(campaign)
            response = self.claim(campaign, user)
            self.check(label + ': claimed', response[0] == 200)
            gift = response[1]['data']
            sid, eid = int(gift['subscription_id']), int(gift['entitlement_id'])
            assert user['id'] in self.users and campaign['id'] in self.campaigns
            # Keep exactly seven days of original duration, three days remaining.
            self.sql(f"BEGIN; UPDATE user_subscription_entitlements SET starts_at=NOW()-INTERVAL '4 days',expires_at=NOW()+INTERVAL '3 days' WHERE id={eid} AND user_subscription_id={sid}; UPDATE user_subscriptions SET starts_at=LEAST(starts_at,NOW()-INTERVAL '4 days'),expires_at=NOW()+INTERVAL '3 days' WHERE id={sid}; COMMIT;")
            if paid:
                self.sql(f"BEGIN; UPDATE user_subscription_entitlements SET expires_at=NOW()-INTERVAL '1 second' WHERE user_subscription_id={sid} AND source_type<>'campaign'; UPDATE subscription_contracts SET expires_at=NOW()-INTERVAL '1 second' WHERE subscription_id={sid}; COMMIT;")
            return user, sid

        def matrix(self):
            for tier, units, old_paid in [('week90', 1, False), ('week180', 2, True)]:
                user, sid = self.gift_user(tier, old_paid)
                key = self.key(user, sid)
                self.check(tier + ': original key before purchase', self.call(key)[0] == 200)
                before = self.await_usage(user, sid, '0.0012')
                self.check(tier + ': server exposes campaign_stack', bool(before.get('campaign_stack')))
                gift_before = self.snapshot_campaign_rows(sid)
                quote = self.quote(user, tier, 'stack', units=units)
                expected = (Decimal(str(self.plans[tier]['price'])) * units * 3 / 7).quantize(Decimal('.01'), rounding=ROUND_HALF_UP)
                self.check(tier + ': prorated price and three days', quote['billable_days'] == 3 and Decimal(str(quote['order_amount'])) == expected)
                self.check(tier + ': exact gift expiry', quote['projected_contract']['expires_at'] == before['campaign_stack']['expires_at'])
                monthly = self.request('/api/v1/payment/subscription-quote', {'plan_id': self.plans['month90']['id'], 'operation': 'stack', 'units': 1}, user['token'])
                self.check(tier + ': monthly stack rejected', monthly[0] == 400)
                order = self.pay(user, quote)
                self.check(tier + ': repeated callback succeeds', self.notify(order)[0] == 200)
                after = self.subscription(user, sid)
                self.check(tier + ': same pool and term', after['id'] == sid and after['contract']['term_id'] == before['contract']['term_id'])
                self.check(tier + ': paid quantity excludes gift', after['contract']['quantity'] == units)
                self.check(tier + ': quota and usage retained', after['quota_summary']['daily_limit_usd'] == 45 + units * self.plans[tier]['daily_limit_usd'] and abs(after['quota_summary']['daily_usage_usd'] - .0012) < 1e-9)
                self.check(tier + ': gift unchanged by purchase', self.snapshot_campaign_rows(sid) == gift_before)
                self.check(tier + ': original key after purchase', self.call(key)[0] == 200)
                self.await_usage(user, sid, '.0024')
                gift_before = self.snapshot_campaign_rows(sid)
                renewal = self.quote(user, tier, 'renew', periods=1)
                self.check(tier + ': renew only paid units', Decimal(str(renewal['order_amount'])) == Decimal(str(self.plans[tier]['price'])) * units)
                self.pay(user, renewal)
                self.check(tier + ': gift unchanged by renewal', self.snapshot_campaign_rows(sid) == gift_before)
                if tier == 'week90':
                    self.pay(user, self.quote(user, 'week180', 'upgrade'))
                    self.check('upgrade leaves gift unchanged', self.snapshot_campaign_rows(sid) == gift_before)
                self.sql(f"UPDATE user_subscription_entitlements SET expires_at=NOW()-INTERVAL '1 second' WHERE user_subscription_id={sid} AND source_type='campaign'")
                expired = self.subscription(user, sid)
                self.check(tier + ': only paid quota after gift expiry', expired['quota_summary']['daily_limit_usd'] == units * 180)
                self.check(tier + ': original key after gift expiry', self.call(key)[0] == 200)

        def run_campaign_stack(self):
            self.campaigns = []
            self.private.mkdir(parents=True, mode=0o700)
            error = None
            try:
                self.guard()
                original_campaigns = self.sql("SELECT row_to_json(c) FROM benefit_campaigns c ORDER BY id")
                original_ids = [int(c['id']) for c in original_campaigns]
                dump = self.private / 'before-stage.dump'
                with dump.open('xb') as out:
                    os.chmod(dump, 0o600)
                    subprocess.run(['docker','exec',stage.PG,'pg_dump','-U',stage.DATABASE,'-d',stage.DATABASE,'-Fc'],stdout=out,check=True,timeout=120)
                self.prepare()
                self.start_mock()
                self.setup()
                self.matrix()
                if args.hold_ui:
                    user, sid = self.gift_user('visual')
                    stage.private_json(self.private / 'browser-user.json', {'email':user['email'],'password':user['fixture_password'],'subscription_id':sid,'plan_ids':{name:p['id'] for name,p in self.plans.items()},'revision':args.expected_revision,'image':args.expected_image})
                    print('UI_READY ' + str(self.private / 'browser-user.json'), flush=True)
                    deadline = time.monotonic() + 1200
                    while not (self.private / 'ui-done').exists() and time.monotonic() < deadline:
                        time.sleep(1)
                restored_campaigns = self.sql('SELECT row_to_json(c) FROM benefit_campaigns c WHERE id IN ('+(','.join(map(str, original_ids)) or '0')+') ORDER BY id')
                self.check('all pre-existing campaigns unchanged', restored_campaigns == original_campaigns)
            except BaseException as exc:
                error = type(exc).__name__ + ': ' + str(exc)
                print('ERROR ' + error, flush=True)
            finally:
                try:
                    self.restore()
                finally:
                    if self.server:
                        self.server.shutdown()
                        self.server.server_close()
                    result = {'revision':args.expected_revision,'image':args.expected_image,'error':error,'checks':self.checks,'restored':bool(self.snapshot and self.snapshot.get('restored')),'production_ready':False,'payment_mode':'synthetic','upstream_mode':'synthetic'}
                    result['passed'] = error is None and result['restored'] and all(c['passed'] for c in self.checks)
                    stage.private_json(self.private / 'campaign-stack-result.json', result)
                    print('RESULT ' + str(self.private / 'campaign-stack-result.json'), flush=True)
            return result['passed']

    fixture = Fixture(args)
    with (stage.LAB / '.v2-synthetic.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
        if args.dry_run or args.restore:
            fixture.execute()
            return
        ok = fixture.run_campaign_stack()
    raise SystemExit(0 if ok else 1)


if __name__ == '__main__':
    main()
