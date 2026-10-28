// The acceptance-real session helper (W2-4 (a)): the way in with no FB
// Console and no demo mint. The default build verifies staff sessions with
// the core's own session key set (identity.SessionKeySet as the verifier's
// KeySource, core/cmd/core/signin.go): an ES256 JWT whose kid is the RFC
// 7638 thumbprint of the box's current EC P-256 key, carrying iss, sub, aud,
// jti, iat and exp and nothing else (identity/sessionsigner.go). Authority
// is never read from the token: per request the core resolves the staff
// user and its winning grant from the database by the subject, the platform
// user id (identity/authority.go). So this helper signs a token exactly the
// way identity.SessionMinter.MintAt does, with the run's throwaway key, for
// a subject the box provisioned as a staff user (core finance-user through
// HH_FINANCE_USERS at boot). Every key is generated per run and lives only
// in the run's state directory; nothing here is a credential of any real
// system.
import { createHash, createSign, generateKeyPairSync, randomBytes, randomUUID } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync, chmodSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

/** The throwaway key pair and its thumbprint, the minter's kid. */
export interface ThrowawayKey {
  readonly privateKeyPem: string;
  readonly publicKeyPem: string;
  readonly kid: string;
}

export interface AcceptanceState {
  readonly boxUrl: string;
  readonly consoleEndpoint: string;
  readonly org: string;
  readonly issuer: string;
  readonly staffAudience: string;
  /** The platform user id the box provisioned; the token's sub. */
  readonly subject: string;
  readonly email: string;
  readonly displayName: string;
  readonly roleCode: string;
  readonly roleName: string;
  readonly key: ThrowawayKey;
}

const stateDir = (): string => {
  const dir = process.env.MDREAL_STATE;
  if (dir === undefined || dir === "") throw new Error("MDREAL_STATE is not set: run through acceptance-real.config.ts or box.sh");
  return dir;
};

export const statePath = (): string => join(stateDir(), "state.json");

export function loadState(): AcceptanceState {
  return JSON.parse(readFileSync(statePath(), "utf8")) as AcceptanceState;
}

/** One invented acceptance tester; nothing here names a real person. */
export function acceptanceTester(): { email: string; displayName: string; roleCode: string; roleName: string } {
  return {
    email: "acceptance.controller@example.invalid",
    displayName: "Acceptance Controller",
    roleCode: "financial_controller",
    roleName: "Financial Controller",
  };
}

function b64url(input: Buffer | Uint8Array): string {
  return Buffer.from(input).toString("base64url");
}

function ecCoordinates(publicKeyPem: string): { x: string; y: string } {
  // The fixed width big-endian coordinates the core's thumbprint builds
  // (identity/sessionkeys.go): 32 bytes each, base64url without padding,
  // read from the SPKI subjectPublicKey BIT STRING.
  const der = Buffer.from(publicKeyPem.replace(/-----[^-]+-----/g, "").replace(/\s+/g, ""), "base64");
  // SPKI: SEQUENCE { SEQUENCE { OID, OID }, BIT STRING }. The EC point is
  // 0x04 || X || Y at the tail of the BIT STRING payload.
  const point = der.subarray(der.length - 65);
  if (point.length !== 65 || point[0] !== 0x04) throw new Error("the throwaway key is not an uncompressed P-256 point");
  return { x: b64url(point.subarray(1, 33)), y: b64url(point.subarray(33, 65)) };
}

/** The RFC 7638 thumbprint kid, byte for byte the core's SessionKeyThumbprint. */
export function sessionKeyThumbprint(publicKeyPem: string): string {
  const { x, y } = ecCoordinates(publicKeyPem);
  const canonical = `{"crv":"P-256","kty":"EC","x":"${x}","y":"${y}"}`;
  return b64url(createHash("sha256").update(canonical, "utf8").digest());
}

/** Generate the run's throwaway EC P-256 key pair (PKCS8, one of the core's accepted forms). */
export function generateThrowawayKey(): ThrowawayKey {
  const { privateKey, publicKey } = generateKeyPairSync("ec", { namedCurve: "P-256" });
  const privateKeyPem = privateKey.export({ type: "pkcs8", format: "pem" }).toString();
  const publicKeyPem = publicKey.export({ type: "spki", format: "pem" }).toString();
  return { privateKeyPem, publicKeyPem, kid: sessionKeyThumbprint(publicKeyPem) };
}

/** Write the private key where box.sh mounts it for the core; owner read only, the core's rule. */
export function writeKeyFile(key: ThrowawayKey): string {
  const dir = join(stateDir(), "keys");
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const path = join(dir, "session.pem");
  // A previous run left the file owner read only; reopen it owner writable
  // before the rewrite, then pin the owner read only mode again.
  try {
    chmodSync(path, 0o600);
  } catch {
    // the file does not exist yet
  }
  writeFileSync(path, key.privateKeyPem, { mode: 0o400 });
  chmodSync(path, 0o400);
  return path;
}

export interface MintedSession {
  readonly token: string;
  readonly expiresAtMs: number;
}

/**
 * Sign a staff session exactly as identity.SessionMinter.MintAt does:
 * header {alg, kid, typ}, claims {iss, sub, aud, jti, iat, exp}, and the
 * signature the fixed width r || s concatenation (ieee-p1363) over the
 * SHA-256 of the ASCII signing input. The window stays inside the minter's
 * bounds (at least one second, at most the driver ceiling) and the staff
 * session ceiling.
 */
export function mintStaffSession(
  key: ThrowawayKey,
  claims: { issuer: string; audience: string; subject: string },
  windowSeconds: number,
): MintedSession {
  if (windowSeconds < 1 || windowSeconds > 168 * 3600) throw new Error("the session window is outside the minter's bounds");
  const iat = Math.floor(Date.now() / 1000);
  const header = b64url(Buffer.from(JSON.stringify({ alg: "ES256", kid: key.kid, typ: "JWT" })));
  const body = b64url(
    Buffer.from(
      JSON.stringify({
        iss: claims.issuer,
        sub: claims.subject,
        aud: claims.audience,
        jti: randomBytes(16).toString("hex"),
        iat,
        exp: iat + windowSeconds,
      }),
    ),
  );
  const signed = `${header}.${body}`;
  // crypto.sign with sha256 hashes the signing input itself, which is the
  // JWS ES256 construction; ieee-p1363 is the raw r || s form the core
  // verifies (identity/verifier.go).
  const sign = createSign("sha256");
  sign.update(signed, "utf8");
  const sig = sign.sign({ key: key.privateKeyPem, dsaEncoding: "ieee-p1363" });
  return { token: `${signed}.${b64url(sig)}`, expiresAtMs: (iat + windowSeconds) * 1000 };
}

// ── the setup CLI: box.sh runs this once per `up` to write the run's state ──
//
//   node --experimental-strip-types session.ts setup --box-port N --console-port N --org slug
//
// The issuer and the audiences are this run's throwaway names; the subject
// is a fresh uuid, the platform user id the box links the acceptance tester
// to. Nothing here names a real person or system.
function setupMain(argv: string[]): void {
  const arg = (name: string): string => {
    const at = argv.indexOf(name);
    if (at < 0 || at + 1 >= argv.length) throw new Error(`setup: ${name} is required`);
    return argv[at + 1];
  };
  const boxPort = arg("--box-port");
  const consolePort = arg("--console-port");
  const org = arg("--org");
  const tester = acceptanceTester();
  const key = generateThrowawayKey();
  writeKeyFile(key);
  const state: AcceptanceState = {
    boxUrl: `http://127.0.0.1:${boxPort}`,
    consoleEndpoint: `http://127.0.0.1:${consolePort}`,
    org,
    issuer: process.env.MDREAL_ISSUER ?? "hh-acceptance-real",
    staffAudience: process.env.MDREAL_STAFF_AUDIENCE ?? "hh-staff",
    // The workflow generates the subject per run and masks it; a developer
    // run draws a fresh uuid.
    subject: process.env.MDREAL_SUBJECT ?? randomUUID(),
    ...tester,
    key,
  };
  writeFileSync(statePath(), `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
}

const invokedAs = process.argv[1] === undefined ? "" : pathToFileURL(process.argv[1]).href;
if (import.meta.url === invokedAs && process.argv[2] === "setup") {
  setupMain(process.argv.slice(3));
}
