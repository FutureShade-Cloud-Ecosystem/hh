// The console legs every converted app shares (item 7d), written once and registered per app by
// defineConsoleLegs from the app's own console.pw.ts. They run in a real browser against the console harness
// (console-harness.ts): the front door and the app on two local ports, the fake FB Console and the core's
// auth routes behind one credentialed cross origin router. Nothing here reads a vault or a real credential.
import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

const EMAIL = "partner.one@hh-demo.invalid";
const PASSWORD = "correct horse battery";
const MIRROR_KEY = "hh-auth.session.v2";
const PERSONA_KEY = "hh-auth.persona.v1";

export interface ConsoleLegs {
  /** The app's name in the test titles. */
  app: string;
  /** The role the fake exchange names, as the app's stub knows the account. */
  role: { role_code: string; role_name: string; user_id: string };
  /** The persona (the core's flow id) a persona hop remembers, and the label of its card at the front door. */
  persona: string;
  personaCard: string;
  /** What the app shows once it is signed in as the role: its existing screen and the role's label. */
  signedIn: (page: Page, roleName: string) => Promise<void>;
  /** An app that resolves its branch at start names the route here; a leg then makes it answer 503 and holds that the console session survives. */
  branchPath?: string;
  /** What the app shows with no session and no front door at the origin root: its unavailable entry. */
  unavailable: (page: Page) => Promise<void>;
}

const authStub = (): string => `http://127.0.0.1:${process.env["CW_AUTH_STUB"]}`;
const world = (): string => `http://127.0.0.1:${process.env["CW_WORLD"]}`;
const frontDoor = (): string => `http://127.0.0.1:${process.env["CW_FRONT_DOOR"]}`;
const appUrl = (): string => `http://127.0.0.1:${process.env["CW_APP"]}`;

function servedConfig(patch: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    mode: "console",
    org: "hh-demo",
    consoleEndpoint: `${world()}/console/v1`,
    consoleProjectId: "fake-project",
    coreBaseUrl: world(),
    personas: false,
    personasOpen: false,
    dealer: { name: "Gable Landscape Supply", mark: "gable" },
    ...patch,
  };
}

async function serve(request: APIRequestContext, legs: ConsoleLegs, patch: Record<string, unknown> = {}): Promise<void> {
  expect((await request.post(`${authStub()}/_stub/reset`)).ok()).toBe(true);
  expect((await request.post(`${authStub()}/_stub/config`, { data: { config: servedConfig(patch) } })).ok()).toBe(true);
  expect((await request.post(`${authStub()}/_stub/console`, { data: { role: legs.role } })).ok()).toBe(true);
}

async function peek(request: APIRequestContext): Promise<{ exchanges: number; jwts: number; personas: { persona: string; authorized: boolean }[]; cookie: boolean }> {
  return (await request.get(`${authStub()}/_stub/console-peek`)).json();
}

async function signInAtTheFrontDoor(page: Page): Promise<void> {
  await page.goto(`${frontDoor()}/`);
  await page.getByLabel("Email").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByTestId("signin-submit").click();
  await expect(page.getByTestId("continue-own")).toBeVisible();
}

/** Every key and value in both storages of the page, for the leg that reads them. */
async function storages(page: Page): Promise<{ local: Record<string, string>; session: Record<string, string> }> {
  return page.evaluate(() => {
    const dump = (store: Storage): Record<string, string> => {
      const out: Record<string, string> = {};
      for (let index = 0; index < store.length; index += 1) {
        const key = store.key(index) as string;
        out[key] = store.getItem(key) ?? "";
      }
      return out;
    };
    return { local: dump(window.localStorage), session: dump(window.sessionStorage) };
  });
}

/** A JWT shaped value, a bearer header word or a stub token anywhere in a storage dump. */
function bearerCapable(dump: Record<string, string>): string[] {
  return Object.entries(dump)
    .filter(([, value]) => /[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}/.test(value) || /Bearer\s/i.test(value) || /stub\.jwt/.test(value))
    .map(([key]) => key);
}

export function defineConsoleLegs(legs: ConsoleLegs): void {
  test.use({ viewport: { width: 1280, height: 900 } });

  test(`${legs.app}: sign in at the front door, tap in, and the app shows the person's role and its existing screen`, async ({ page, request }) => {
    await serve(request, legs);
    await signInAtTheFrontDoor(page);
    await page.getByTestId("continue-own").click();
    const before = await peek(request);
    await page.goto(`${appUrl()}/`);
    await legs.signedIn(page, legs.role.role_name);
    const after = await peek(request);
    expect(after.exchanges).toBeGreaterThan(before.exchanges); // the app recovered its own session
    expect(after.jwts).toBeGreaterThan(before.jwts);
    expect(after.personas).toEqual([]); // no persona was asked for
  });

  test(`${legs.app}: a reload recovers the session again, with nothing carried between pages`, async ({ page, request }) => {
    await serve(request, legs);
    await signInAtTheFrontDoor(page);
    await page.goto(`${appUrl()}/`);
    await legs.signedIn(page, legs.role.role_name);
    await page.reload();
    await legs.signedIn(page, legs.role.role_name);
  });

  test(`${legs.app}: nothing bearer capable is in either storage on the app page`, async ({ page, request }) => {
    await serve(request, legs);
    await signInAtTheFrontDoor(page);
    // The front door page too: only the display profile may be there.
    const fromFrontDoor = await storages(page);
    expect(bearerCapable(fromFrontDoor.local)).toEqual([]);
    expect(bearerCapable(fromFrontDoor.session)).toEqual([]);
    await page.goto(`${appUrl()}/`);
    await legs.signedIn(page, legs.role.role_name);
    const dump = await storages(page);
    expect(dump.local).toEqual({});
    expect(bearerCapable(dump.session)).toEqual([]);
    expect(Object.keys(dump.session).filter((key) => key !== MIRROR_KEY)).toEqual([]);
    // The front door's demo handoff key is never written in console mode.
    expect(Object.keys(dump.session)).not.toContain("hh-demo.front-door.session.v1");
    // The one value that is there is the display profile.
    expect(Object.keys(JSON.parse(dump.session[MIRROR_KEY] ?? "{}")).sort()).toEqual(["email", "name", "userId", "v"]);
  });

  test(`${legs.app}: with no console session the app holds no bearer and shows its unavailable entry`, async ({ page, request }) => {
    await serve(request, legs);
    await page.goto(`${appUrl()}/`);
    await legs.unavailable(page);
    const dump = await storages(page);
    expect(dump.local).toEqual({});
    expect(bearerCapable(dump.session)).toEqual([]);
  });

  test(`${legs.app}: a persona hop leaves exactly the persona id in session storage and asks the core for its own session`, async ({ page, request }) => {
    await serve(request, legs, { personas: true, personasOpen: true });
    await page.goto(`${frontDoor()}/`);
    await page.getByTestId("persona-cards").getByRole("button", { name: legs.personaCard, exact: true }).click();
    await expect.poll(async () => (await peek(request)).personas.map((call) => call.persona)).toEqual([legs.persona]);
    // The front door writes the persona id after the core's answer is adopted, a beat after the stub saw the call.
    await expect
      .poll(async () => JSON.parse((await storages(page)).session[PERSONA_KEY] ?? "null"))
      .toBe(legs.persona);
    const onFrontDoor = await storages(page);
    expect(onFrontDoor.local).toEqual({});
    expect(JSON.parse(onFrontDoor.session[PERSONA_KEY] ?? "null")).toBe(legs.persona);
    expect(bearerCapable(onFrontDoor.session)).toEqual([]);
    const asked = (await peek(request)).personas.length;
    if (legs.branchPath !== undefined) {
      // The app stubs know the demo accounts, not persona subjects: let the branch read through so the leg
      // looks at the storage, not at an ending refusal the stub would answer a persona.
      await page.route(`${world()}${legs.branchPath}**`, (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: '[{"id":"11111111-1111-4111-8111-111111111111","code":"MAIN","name":"Main branch"}]' }),
      );
    }
    // The app page, in the same tab's session storage: copy the front door's session storage over, as the
    // browser does for a link in the same tab (the two local ports are two origins in a test only).
    await page.goto(`${appUrl()}/`, { waitUntil: "domcontentloaded" });
    await page.evaluate(
      ([key, value]) => window.sessionStorage.setItem(key as string, value as string),
      [PERSONA_KEY, JSON.stringify(legs.persona)],
    );
    await page.reload();
    await expect.poll(async () => (await peek(request)).personas.length).toBeGreaterThan(asked);
    const hop = (await peek(request)).personas.at(-1);
    expect(hop?.persona).toBe(legs.persona);
    expect(hop?.authorized).toBe(false); // the public copy sends no bearer
    const onApp = await storages(page);
    expect(onApp.local).toEqual({});
    expect(bearerCapable(onApp.session)).toEqual([]);
    expect(JSON.parse(onApp.session[PERSONA_KEY] ?? "null")).toBe(legs.persona);
    expect(Object.keys(onApp.session).filter((key) => key !== MIRROR_KEY && key !== PERSONA_KEY)).toEqual([]);
  });

  if (legs.branchPath !== undefined) {
    const branchPath = legs.branchPath;
    test(`${legs.app}: a branch that does not resolve at boot (core 503) does not delete the person's console session`, async ({ page, request }) => {
      await serve(request, legs);
      await signInAtTheFrontDoor(page);
      const deletes: string[] = [];
      page.on("request", (made) => {
        if (made.method() === "DELETE" && made.url().includes("/console/v1/account/sessions")) deletes.push(made.url());
      });
      await page.route(`${world()}${branchPath}**`, (route) => route.fulfill({ status: 503, contentType: "application/json", body: '{"error":"Service Unavailable"}' }));
      await page.goto(`${appUrl()}/`);
      await legs.unavailable(page);
      await page.waitForTimeout(2500); // the custody's watch runs every two seconds: it must not read this as an end
      expect(deletes).toEqual([]);
      expect((await peek(request)).cookie).toBe(true); // the console session is still there
    });
  }
}
