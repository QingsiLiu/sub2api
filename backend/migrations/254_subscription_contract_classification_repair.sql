-- Migration 253 is immutable. Repair only untouched legacy migration contracts
-- that cannot faithfully be represented as one currently purchasable unit.
-- Purchased V2 changes, identifiers, dates, request bindings and financial
-- counters are preserved. Historical rights continue through legacy_daily.
UPDATE subscription_contracts c
SET mode='legacy_daily', kind='', unit_daily_usd=0, quantity=0, period_days=0,
    is_current=FALSE, revision=c.revision+1, updated_at=NOW()
FROM user_subscriptions s
WHERE s.id=c.subscription_id
  AND c.mode='v2' AND c.revision=1 AND c.term_id='legacy-'||s.id::TEXT
  AND c.expires_at>NOW()
  AND NOT EXISTS(SELECT 1 FROM subscription_contract_changes ch WHERE ch.subscription_id=c.subscription_id)
  AND (
    s.starts_at>NOW()
    OR (SELECT COUNT(*) FROM user_subscription_entitlements e
        WHERE e.user_subscription_id=s.id AND e.expires_at>NOW()
          AND e.status NOT IN ('refunded','revoked')) <> 1
    OR NOT EXISTS(
      SELECT 1 FROM user_subscription_entitlements e
      JOIN subscription_plans p ON p.id=COALESCE(e.plan_id,s.plan_id)
      LEFT JOIN payment_orders o ON o.id=e.source_order_id
      WHERE e.user_subscription_id=s.id AND e.status='active'
        AND e.starts_at<=NOW() AND e.expires_at>NOW()
        AND NOT p.is_legacy_compat AND p.price>0
        AND p.daily_limit_usd=e.daily_limit_usd
        AND (o.subscription_days IS NULL OR o.subscription_days<=0 OR o.subscription_days=
          CASE WHEN p.validity_unit IN ('week','weeks') THEN p.validity_days*7
               WHEN p.validity_unit IN ('month','months') THEN p.validity_days*30 ELSE p.validity_days END)
    )
  );
