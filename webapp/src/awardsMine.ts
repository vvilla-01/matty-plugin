import {fetchMyAwards} from './client';
import {askOwn, ownGeneration} from './ownAnswers';
import type {AwardsMine} from './types';

// The reader's own awards and choice (GET /api/v1/awards/mine), for the
// Trophies card's You tab: the last answer this page had, kept for the
// reader it was asked for, so the picker draws the chips at once on every
// opening after the first, and keeps them when a later read fails, as it did
// when they came with the snapshot. Kept with the boards' version it was
// asked under, and good only for as long as that stands: what somebody holds
// changes when that word does, and an older answer could draw an award since
// lost. Asked when the card opens and nothing good is kept, so that the tab
// usually has it by the time it is chosen.
//
// One of the reader's own answers (ownAnswers.ts): asked through askOwn, and
// kept only for the sign-in it was asked in, so a sign-out drops it and it is
// never shown to whoever signs in next.

let kept: {userId: string; generation: number; version: string; mine: AwardsMine} | null = null;
let asking: {userId: string; generation: number; request: Promise<AwardsMine | null>} | null = null;

// The last answer for this reader, if it was asked under the boards as they
// now stand (`version`, boards.version), or null.
export function keptAwardsMine(userId: string, version: string): AwardsMine | null {
    if (kept && (kept.generation !== ownGeneration() || kept.userId !== userId)) {
        kept = null;
    }

    return kept && kept.version === version ? kept.mine : null;
}

// Asks the server, and keeps what it answers. A read already on its way for
// the same reader is shared, unless `fresh` asks for one that starts now
// (the boards have changed since that one left). Rejects as the request
// does; null when the reader signed out meanwhile.
export function askAwardsMine(userId: string, version: string, fresh = false): Promise<AwardsMine | null> {
    const generation = ownGeneration();
    if (!fresh && asking && asking.userId === userId && asking.generation === generation) {
        return asking.request;
    }

    const request: Promise<AwardsMine | null> = askOwn(fetchMyAwards).then((answer) => {
        if (answer) {
            kept = {userId, generation, version, mine: answer};
        }

        return answer;
    });
    const settled = () => {
        if (asking && asking.request === request) {
            asking = null;
        }
    };
    request.then(settled, settled);
    asking = {userId, generation, request};

    return request;
}

// The Trophies card opening for a linked reader: asked once now if nothing
// good for these boards is kept or on its way, quietly.
export function readyAwardsMine(userId: string, version: string): void {
    if (keptAwardsMine(userId, version) || (asking && asking.userId === userId && asking.generation === ownGeneration())) {
        return;
    }

    askAwardsMine(userId, version).catch(() => {
        // The You tab asks again when it opens, and says so if that fails.
    });
}
