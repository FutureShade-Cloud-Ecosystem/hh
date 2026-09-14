# @hh/auth

JWT verified against the platform JWK Set; org resolved from the `X-Org`
header validated against Team membership before any database is touched.
Offline identity is a cached session plus device unlock (PIN or biometric) for
shared devices, and the audit records the unlocked user.
