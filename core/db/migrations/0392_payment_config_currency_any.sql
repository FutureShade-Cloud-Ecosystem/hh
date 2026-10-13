-- 0392: payment_config.currency is the dealer's currency, no longer CAD only (item 3b slice 2).
--
-- 0027 pinned the card rail row to CAD (CHECK (currency = 'CAD')) because every dealer was an
-- Ontario dealer. The currency is now the dealer's own (system_settings.currency, read through the
-- settings module's dealer configuration); this migration replaces the constant check with the shape
-- check the dealer configuration itself enforces on write: three uppercase letters, an ISO 4217 code.
-- The column default stays 'CAD' so a row written without a currency is still the Ontario one, and
-- every existing row (all CAD) stays valid. No column, no table and no row is added, so the DX-19
-- currency column scan sees nothing new.
--
-- The 0027 check is inline on the column, so Postgres named it payment_config_currency_check.
-- Owner module: internal/payment.

ALTER TABLE payment_config DROP CONSTRAINT payment_config_currency_check;
ALTER TABLE payment_config
  ADD CONSTRAINT payment_config_currency_check CHECK (currency ~ '^[A-Z]{3}$');
