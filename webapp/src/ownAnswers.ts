// What the panel is told in the reader's own name, and the 3.1.0 rule for it
// (design/3.2.0.md 2.6): signing out of Mattermost does not reload the page,
// so whatever was told in somebody's name is dropped on sign-out and when the
// plugin goes, and a request still on its way cannot bring it back for
// whoever signs in next.
//
// 3.2.0's own answers — the reader's choice of awards (GET /awards/mine),
// their achievement progress (GET /achievements/mine),
// their own popover's takebacks (GET /takebacks) and the soundboard's list and
// voice state (GET /sounds) — are held by the components that show them, which
// go when the panel and the popover do. What they ask goes through askOwn,
// which answers null for an answer that arrives after somebody signed out, so
// none of it can land in a component of the next reader's. The soundboard's
// "somebody else's sound is playing", which the WebSocket tells the page, is
// soundboard.ts's, dropped the same way.
//
// Imports nothing, so build/check-away.mjs holds it to the rule.

let generation = 0;

// Asked in the reader's own name: the answer, or null when the reader has
// signed out (or the plugin gone) since it was asked.
export function askOwn<T>(ask: () => Promise<T>): Promise<T | null> {
    const asked = generation;

    return ask().then((answer) => (asked === generation ? answer : null));
}

// Whether an answer asked at generation `asked` still belongs to the reader.
export const ownGeneration = (): number => generation;

// Sign-out, and uninitialize: every answer on its way is answered to nobody.
export function forgetOwnAnswers(): void {
    generation += 1;
}
