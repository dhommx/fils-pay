-- Additional suspense accounts for future reconciliation lanes.
-- 0007's chart has no 'suspense' kind; suspense accounts are modelled as
-- assets, the same as scheme_rounding_residue.

INSERT INTO chart_of_accounts (id, name, kind)
VALUES
  ('fx_rounding_difference', 'FX Rounding Difference (suspense)', 'asset'),
  ('chargeback_suspense', 'Chargeback Suspense', 'asset')
ON CONFLICT (id) DO NOTHING;
