// What the page has heard about the soundboard outside its sheet: when
// somebody's sound will have finished, from the sound event the server sends
// to the people matched in the voice roster as a sound starts and as it ends
// (design/3.2.0.md 2.3, 7.10). It names no sound and no person; the sheet uses
// it to grey the tiles while something plays, and asks GET /sounds again when
// it moves.
//
// Dropped on sign-out and when the plugin goes, with everything else told in
// the reader's own name (ownAnswers.ts). No React here, so check-away.mjs can
// load it.

type Listener = () => void;

let busy = {until: 0, heardAt: 0};
const listeners = new Set<Listener>();

export function getSoundBusy(): {until: number; heardAt: number} {
    return busy;
}

// The sound event's data, {"busyUntil": ms}, when the page heard it; or the
// same as a JSON string under "payload", the way the streams event carries
// the snapshot.
function busyUntilOf(data: unknown): unknown {
    if (typeof data !== 'object' || data === null) {
        return undefined;
    }

    const fields = data as {busyUntil?: unknown; payload?: unknown};
    if (typeof fields.payload === 'string') {
        try {
            return busyUntilOf(JSON.parse(fields.payload));
        } catch {
            return undefined;
        }
    }

    return fields.busyUntil;
}

export function heardSound(data: unknown, now = Date.now()): void {
    const until = busyUntilOf(data);
    if (typeof until !== 'number' || !Number.isFinite(until) || until < 0) {
        return;
    }

    busy = {until, heardAt: now};
    Array.from(listeners).forEach((listener) => listener());
}

export function onSoundBusy(listener: Listener): () => void {
    listeners.add(listener);

    return () => {
        listeners.delete(listener);
    };
}

export function forgetSoundboard(): void {
    busy = {until: 0, heardAt: 0};
    Array.from(listeners).forEach((listener) => listener());
}
