// Compile time only (not part of the package's runtime surface): the real
// statechart must fit the runner's idea of a machine, and every event the
// runner can send must be one the machine accepts.
import * as machine from "../../../../statecharts/sync/machine.js";
import type { RunnerEvent, SyncMachine } from "../src/runner.js";

export const fits: SyncMachine = machine;

type Same<A, B> = [A] extends [B] ? ([B] extends [A] ? true : never) : never;
export const eventsAgree: Same<RunnerEvent, machine.SyncEvent> = true;
