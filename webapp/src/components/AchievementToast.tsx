import React from 'react';

import {stepName, stepToSay} from '../awardLogic';
import type {StepsSeen} from '../awardLogic';
import {boardsRead} from '../boards';
import {useBoards} from '../useBoards';

import {AchievementBadge, glyphOf} from './badges';

// A new achievement step, said quietly in the earner's own unlocked panel and
// nowhere else (design/3.2.0.md 5.4): over the footer's bar for six seconds,
// on the bar's own glass, with the step's coin. Nothing is posted or sent.
//
// The browser remembers the steps it has already said, per person. The first
// time it sees somebody's achievements it records them without a word, so the
// backdated ones do not arrive as a burst. Storage that does not work means no
// toasts, never repeated ones. Two new steps at once are two toasts, one after
// the other: each is recorded as said when it is queued, and each is shown.

const SEEN_KEY = 'villanet.achSeen.';
const SHOW_MS = 6000;

function readSeen(userId: string): StepsSeen | null {
    try {
        const raw = window.localStorage.getItem(SEEN_KEY + userId);

        return raw ? JSON.parse(raw) as StepsSeen : null;
    } catch {
        return null;
    }
}

function writeSeen(userId: string, seen: StepsSeen): void {
    try {
        window.localStorage.setItem(SEEN_KEY + userId, JSON.stringify(seen));
    } catch {
        // Not kept: the next page sees everything as for the first time, and
        // says nothing.
    }
}

type Toast = {family: string; tier: number; key: number};

export const AchievementToast: React.FC<{userId: string}> = ({userId}) => {
    // Who holds which step is with the boards, which this page reads when
    // it starts and again whenever the snapshot says they have changed
    // (boards.ts): a new step is said within moments of being reached, as
    // when the achievements rode the snapshot.
    const boards = useBoards();
    const [queue, setQueue] = React.useState<Toast[]>([]);
    const [leaving, setLeaving] = React.useState(false);
    const toast = queue.length > 0 ? queue[0] : null;
    const made = React.useRef(0);

    const show = React.useCallback((family: string, tier: number) => {
        made.current += 1;
        const key = made.current;
        setQueue((waiting) => [...waiting, {family, tier, key}]);
    }, []);

    // A step higher than the one this browser last said, for this person,
    // by awardLogic's stepToSay. Nothing is looked at until the boards have
    // really been read, with achievements on: the store's empty boards, a
    // failed first read and a switched-off feature carry no achievements,
    // and recording that as "holds nothing" would make the first real
    // answer's backdated steps look new.
    const ready = boardsRead();
    const kinds = boards.achievementKinds || [];
    const holder = (boards.achievements || []).find((entry) => entry.userId === userId);
    const steps = JSON.stringify([kinds.map((kind) => kind.id), holder ? holder.have : []]);
    React.useEffect(() => {
        if (!ready || kinds.length === 0) {
            return;
        }

        const {keep, says} = stepToSay(readSeen(userId), kinds, holder ? holder.have : []);
        writeSeen(userId, keep);
        says.forEach((step) => show(step.id, step.tier));
    }, [userId, steps, ready]); // eslint-disable-line react-hooks/exhaustive-deps

    // Each toast for its six seconds, then the next.
    const showing = toast ? toast.key : 0;
    React.useEffect(() => {
        if (showing === 0) {
            return undefined;
        }

        setLeaving(false);
        const fade = window.setTimeout(() => setLeaving(true), SHOW_MS - 300);
        const gone = window.setTimeout(() => setQueue((waiting) => waiting.slice(1)), SHOW_MS);

        return () => {
            window.clearTimeout(fade);
            window.clearTimeout(gone);
        };
    }, [showing]);

    const kind = toast ? kinds.find((entry) => entry.id === toast.family) : undefined;

    return (
        <div
            className='vntv-achtoast-host'
            role='status'
        >
            {toast && kind ? (
                <div
                    key={toast.key}
                    className='vntv-achtoast'
                    data-leaving={leaving ? 'true' : undefined}
                >
                    <span
                        className='vntv-achtoast-coin'
                        aria-hidden={true}
                    >
                        <AchievementBadge
                            kind={glyphOf(kind.id)}
                            tier={toast.tier}
                            size={28}
                        />
                    </span>
                    <span className='vntv-achtoast-words'>
                        <span className='vntv-achtoast-eyebrow'>{'New achievement'}</span>
                        <span className='vntv-achtoast-name'>{stepName(kind, toast.tier)}</span>
                    </span>
                </div>
            ) : null}
        </div>
    );
};
