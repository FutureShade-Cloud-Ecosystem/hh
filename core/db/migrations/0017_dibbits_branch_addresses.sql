-- 0017: real Dibbits branch addresses + contact (Q-028 answered on the 2026-06-16 call).
-- Phone 613.394.3478 · scrubbed-0001@example.invalid
--   Trenton:  132 County Road 40, Trenton, ON K8V 5P7
--   Kingston: 2167 County Rd. 38, Kingston, ON K7P 2Y7

INSERT INTO system_settings (key, value) VALUES
  ('org_phone', '"613.394.3478"'),
  ('org_email', '"scrubbed-0001@example.invalid"')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();

UPDATE locations SET address = jsonb_build_object(
    'line1','132 County Road 40','city','Trenton','province','ON','postal','K8V 5P7','country','CA'),
    updated_at = now()
  WHERE id = '5d1b3f6e-0000-4000-9000-000000000001';

UPDATE locations SET address = jsonb_build_object(
    'line1','2167 County Rd. 38','city','Kingston','province','ON','postal','K7P 2Y7','country','CA'),
    updated_at = now()
  WHERE id = '5d1b3f6e-0000-4000-9000-000000000002';
