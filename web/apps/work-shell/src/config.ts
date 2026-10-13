// The host's fixed configuration. Nothing here is a secret.

/** The organisation the Console exchange is asked for: the demo tenant's slug, the front door's own constant. */
export const DEMO_ORG_SLUG = "hh-demo";

/** The organisation word in the band: the demo tenant (GLS-TENANT), until a sign in answers a dealer name. */
export const DEMO_ORG_NAME = "Gable Landscape Supply";

/** Where the front door is served inside the Work profile (assemble-dist.sh builds it at this base). */
export const DOOR_PATH = "/door/";

/** The two files the assembly script ships beside the host. */
export const PROFILE_FILE = "/profile.json";
export const LAUNCH_FILE = "/launch.json";

/**
 * The longest Switch role waits for the framed app to say its own sign out has
 * run. After this the host revokes what it can reach without the answer, so a
 * frame that is busy, offline or broken never holds the person at the desk.
 */
export const SIGN_OUT_WAIT_MS = 2000;
