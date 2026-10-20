// The panel's seam to the core: the four staff operations of PANEL-API r1 section 1, and nothing else. The
// panel holds a TanyaiTransport and never a client of its own; the shell hands it one built from the
// generated client (generatedTransport), and a test hands it a stub. Each call resolves a Result and never
// throws, exactly as the generated operations do.
//
// A transport may also offer chatStream (section 3): the same route with Accept text/event-stream. The
// generated client does not stream, so generatedTransport leaves it out until the core ships it; the panel
// uses it when it is present and falls back to chat when it is not.

import { staffAssist, type ApiClient, type Result } from '@hardscapehouse/api-client';
import type { HistoryMessage, TanyaiAnswer, TanyaiCommandInfo, TanyaiResult, TanyaiStreamEvent } from './tanyai-view.js';

export interface TanyaiMe {
  chat_enabled: boolean;
  spend_visible: boolean;
  over_budget?: boolean;
}

export interface TanyaiCommandList {
  commands: (TanyaiCommandInfo & { summary: string })[];
  chat_enabled: boolean;
}

export interface TanyaiChatBody {
  question: string;
  history?: HistoryMessage[];
}

export interface TanyaiTransport {
  me(): Promise<Result<TanyaiMe>>;
  commands(): Promise<Result<TanyaiCommandList>>;
  chat(body: TanyaiChatBody, signal?: AbortSignal): Promise<Result<TanyaiAnswer>>;
  run(command: string, args: Record<string, string>, signal?: AbortSignal): Promise<Result<TanyaiResult>>;
  chatStream?(body: TanyaiChatBody, signal?: AbortSignal): AsyncIterable<TanyaiStreamEvent>;
}

/** The transport over the generated client. The staff routes only; the Pro route is a later slice. */
export function generatedTransport(client: ApiClient): TanyaiTransport {
  return {
    me: () => staffAssist.assistGetMe(client),
    commands: () => staffAssist.assistListCommands(client),
    chat: (body, signal) => staffAssist.assistChat(client, body, { signal }),
    run: (command, args, signal) => staffAssist.assistRunCommand(client, { command, args }, { signal }),
  };
}
