import React from 'react';

import {clipErrorCopy, shareConfirm, shareDone} from '../clipLogic';
import type {ShareLabel} from '../clipLogic';
import {sharePlan} from '../client';
import {clipErrorCodeOf, clipErrorTextOf, shareOn} from '../clips';
import {currentChannelId, mattermostStore, shareTargetOf} from '../mattermost';
import {nameNow} from '../name';
import {useTwoClicks} from '../twoClicks';

// Share (addendum 4.10): a clip posted into the channel the reader has open,
// as them, as a card everybody in it sees. In the viewer's row of actions,
// before Report and Delete, and in the editor once a clip is published. Never
// in the System Console, which has no channel, and never for somebody who is
// not linked: neither place it lives is open to them.
//
// It names where the clip will go, from Mattermost's own store, and follows
// the reader there: open another channel and it says that one. A shared
// channel (another server's people in it) is refused, here and by the server,
// and so is nowhere at all (the Threads view has no channel).
//
// Two presses, as everything that cannot be undone here takes (twoClicks.ts):
// the first says where it will go, "Click again to post it in Town Square".
// Then "Sharing…", and "Shared" until another channel is opened, with a line
// saying where it went. A share whose answer was lost is safe to send again:
// the server answers the same share within a minute with the post it already
// made.
//
// The button and its lines are two pieces, since the button sits in a row of
// buttons and its lines go under the row: useShareClip holds what they share.
//
// 3.2.0 shares plans the same way (design/3.2.0.md 6.6): useShare takes what
// is shared, a clip or a plan, and everything else is the clip share's, its
// words included, but for the plan's own refusals (6.7).

type Target = {channelId: string; label: ShareLabel};

function readTarget(): Target {
    const channelId = currentChannelId();

    return {channelId, label: shareTargetOf(channelId)};
}

const sameTarget = (a: Target, b: Target): boolean => a.channelId === b.channelId &&
    a.label.label === b.label.label && a.label.title === b.label.title && a.label.disabled === b.label.disabled;

// Where a share would go now, kept up with Mattermost's store. Its store
// changes for everything — a keystroke in the message box is a change — so
// this re-renders only when the channel, or what the button says about it,
// does.
export function useShareTarget(): Target {
    const [target, setTarget] = React.useState(readTarget);

    React.useEffect(() => {
        const store = mattermostStore();
        const check = () => setTarget((was) => {
            const next = readTarget();

            return sameTarget(was, next) ? was : next;
        });

        check();

        return store ? store.subscribe(check) : undefined;
    }, []);

    return target;
}

// A share that did not go: what to say, whether trying again could help, and
// the channel it was for, which is the only one Try again posts into.
type ShareFailure = {text: string; retry: boolean; channelId: string};

export type ShareControl = {
    target: Target;
    words: string;
    title: string;
    disabled: boolean;
    armed: boolean;
    done: string;
    failure: ShareFailure | null;
    button: React.MutableRefObject<HTMLButtonElement | null>;
    press: (event: React.MouseEvent<HTMLButtonElement>) => void;
    retry: (event: React.MouseEvent<HTMLButtonElement>) => void;
};

export type Shared = {kind: 'clip' | 'plan'; id: string};

// A plan share's refusal, in the contract's words (design/3.2.0.md 6.7):
// its own for a plan that has gone and for the meter, the clip share's for
// everything else, with "plan" for "clip".
function planShareCopy(code: string, serverText: string): {text: string; retry: boolean} {
    if (code === 'gone' || code === 'clip_gone') {
        return {text: 'That plan isn’t there any more.', retry: false};
    }
    if (code === 'rate_limited') {
        return {text: 'You’ve shared a lot just now. Try again in a few minutes.', retry: false};
    }

    const copy = clipErrorCopy(code, 'share', {brand: nameNow()}, serverText);

    return {text: copy.text.replace('The clip could not', 'The plan could not'), retry: copy.actions.includes('retry')};
}

export function useShareClip(clipId: string): ShareControl {
    return useShare({kind: 'clip', id: clipId});
}

export function useShare(what: Shared): ShareControl {
    const target = useShareTarget();
    const twice = useTwoClicks();
    const button = React.useRef<HTMLButtonElement | null>(null);
    const [phase, setPhase] = React.useState<'idle' | 'sharing' | 'shared'>('idle');
    const [sharedIn, setSharedIn] = React.useState('');
    const [done, setDone] = React.useState('');
    const [failure, setFailure] = React.useState<ShareFailure | null>(null);
    const alive = React.useRef(true);

    React.useEffect(() => () => {
        alive.current = false;
    }, []);

    // "Shared" until another channel is opened, when the button offers that
    // one (and that one again on the way back), and a first press made for
    // the last one no longer counts. Worked out from the channel too, for a
    // share that lands after the reader has moved on. The line saying where
    // the clip went stays, as a record, until the next share. One saying a
    // share failed goes, with its Try again: that press would post into a
    // channel nobody pressed twice for (addendum 4.10). (disarm is the same
    // function from one render to the next.)
    const {disarm} = twice;
    React.useEffect(() => {
        disarm();
        setPhase((was) => (was === 'shared' ? 'idle' : was));
        setFailure((was) => (was && was.channelId !== target.channelId ? null : was));
    }, [target.channelId, disarm]);

    const sharing = phase === 'sharing';
    const shared = phase === 'shared' && target.channelId === sharedIn;

    const share = () => {
        const into = target.channelId;
        const where = target.label.where;
        setPhase('sharing');
        setDone('');
        setFailure(null);
        (what.kind === 'plan' ? sharePlan(what.id, into) : shareOn(what.id, into)).then(() => {
            if (alive.current) {
                setPhase('shared');
                setSharedIn(into);
                setDone(shareDone(where));
            }
        }, (reason: unknown) => {
            if (!alive.current) {
                return;
            }

            setPhase('idle');
            if (what.kind === 'plan') {
                const copy = planShareCopy(clipErrorCodeOf(reason), clipErrorTextOf(reason));
                setFailure({text: copy.text, retry: copy.retry, channelId: into});

                return;
            }

            const copy = clipErrorCopy(clipErrorCodeOf(reason), 'share', {brand: nameNow()}, clipErrorTextOf(reason));
            setFailure({text: copy.text, retry: copy.actions.includes('retry'), channelId: into});
        });
    };

    const disabled = target.label.disabled || sharing || shared;

    const press = (event: React.MouseEvent<HTMLButtonElement>) => {
        if (disabled || !twice.press('share', event)) {
            return;
        }

        share();
    };

    // Try again: sent at once, since the reader already pressed twice — for
    // that channel, which is still the one open (the failure goes when
    // another is). It goes away as it is pressed, so focus on it goes to
    // Share.
    const retry = (event: React.MouseEvent<HTMLButtonElement>) => {
        if (document.activeElement === event.currentTarget) {
            button.current?.focus({preventScroll: true});
        }
        if (!disabled && failure?.channelId === target.channelId) {
            share();
        }
    };

    const armed = twice.armed === 'share' && !disabled;
    let words = target.label.label;
    if (sharing) {
        words = 'Sharing…';
    } else if (shared) {
        words = 'Shared';
    } else if (armed) {
        words = shareConfirm(target.label.where);
    }

    return {
        target,
        words,
        title: target.label.title,
        disabled,
        armed,
        done,
        failure,
        button,
        press,
        retry,
    };
}

// The button itself, in whatever row of buttons it is put in. aria-disabled
// rather than disabled, so focus stays on it while it shares.
export const ShareClipButton: React.FC<{share: ShareControl; className: string}> = ({share, className}) => (
    <button
        ref={share.button}
        type='button'
        className={`${className} vntv-share`}
        aria-disabled={share.disabled ? true : undefined}
        data-armed={share.armed ? 'true' : undefined}
        title={share.title || undefined}
        onClick={share.press}
    >{share.words}</button>
);

// Its lines, under the row: where it went, or why it did not, with Try again
// when trying again could help. Always in the page, so what is put in them is
// announced; one box for both, so the two take one place in a column.
export const ShareClipStatus: React.FC<{share: ShareControl}> = ({share}) => (
    <div className='vntv-share-status'>
        <p role='status'>{share.done}</p>

        {/* What the first press changed the button to, read out, as a
            change of the focused button's name is not by every screen
            reader: heard, not seen, since the button already shows it. */}
        <p
            className='vntv-sr-only'
            role='status'
        >{share.armed ? `${share.words}.` : ''}</p>
        <p
            className='vntv-notice-error'
            role='alert'
        >
            {share.failure ? <span>{share.failure.text}</span> : null}
            {share.failure?.retry ? (
                <button
                    type='button'
                    className='vntv-pill'
                    onClick={share.retry}
                >{'Try again'}</button>
            ) : null}
        </p>
    </div>
);
