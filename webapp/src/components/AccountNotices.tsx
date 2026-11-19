import React from 'react';

import {lostWords, noticesOf} from '../accountsLogic';
import type {Notice, Service} from '../accountsLogic';
import {dismissAccountNotice} from '../client';
import {refreshIdentity} from '../identity';
import type {Me} from '../types';

import {TwitchLostNotice} from './TwitchConnect';

function errorText(reason: unknown): string {
    return reason instanceof Error ? reason.message : String(reason);
}

// The day, as Twitch's notice says it.
function when(at: number): string {
    try {
        return new Date(at).toLocaleDateString(undefined, {day: 'numeric', month: 'short', year: 'numeric'});
    } catch {
        return '';
    }
}

// Their Steam or Hytale account was proved by somebody else and moved to
// them: said over the bar, on the same glass as the bar's other warning,
// until they say they have read it, so a hijack cannot go unnoticed.
// TwitchLostNotice's words and look, for the other two.
const LostNotice: React.FC<{notice: Notice & {service: Service}}> = ({notice}) => {
    const [sending, setSending] = React.useState(false);
    const [error, setError] = React.useState('');

    const dismiss = () => {
        if (sending) {
            return;
        }

        setSending(true);
        setError('');
        dismissAccountNotice(notice.service).then(() => {
            void refreshIdentity();
        }).catch((reason: unknown) => {
            setError(errorText(reason));
            setSending(false);
        });
    };

    return (
        <div
            className='vntv-bar-alert vntv-twitch-lost'
            role='status'
        >
            <span className='vntv-bar-alert-text'>
                {lostWords(notice.service, notice.name, when(notice.at))}
                {error !== '' ? <span className='vntv-twitch-error'>{` ${error}`}</span> : null}
            </span>
            <button
                type='button'
                className='vntv-sheet-action'
                aria-disabled={sending ? true : undefined}
                onClick={dismiss}
            >{'OK'}</button>
        </div>
    );
};

// The takeover notices over the linked bar (design/3.4.0.md 5.6): one line,
// the newest first; OK shows the next. Twitch's own notice for Twitch's, in
// the same slot. None for a service the reader is blocked from, as nothing
// else of it shows them.
//
// OK takes its notice away, and with it the button that had focus: focus
// goes to the next notice's OK, or, after the last, to the panel's root,
// never to the page's body, from where a letter would go to Mattermost's
// message box. The slot is no box of its own (display: contents), so the
// notice sits in the footer as Twitch's always has.
export const AccountNotices: React.FC<{me: Me}> = ({me}) => {
    const notices = noticesOf(me);
    const first = notices[0];
    const shown = first ? `${first.service}:${first.at}` : '';
    const slotRef = React.useRef<HTMLDivElement | null>(null);
    const hadFocus = React.useRef(false);

    React.useLayoutEffect(() => {
        if (!hadFocus.current) {
            return;
        }

        hadFocus.current = false;
        const active = document.activeElement;
        if (active && active !== document.body) {
            return;
        }

        const next = slotRef.current?.querySelector<HTMLElement>('.vntv-sheet-action');
        (next || slotRef.current?.closest<HTMLElement>('.vntv-panel'))?.focus({preventScroll: true});
    }, [shown]);

    let notice: React.ReactNode = null;
    if (first?.service === 'twitch') {
        notice = me.twitch?.lost ? <TwitchLostNotice lost={me.twitch.lost}/> : null;
    } else if (first) {
        notice = (
            <LostNotice
                key={shown}
                notice={first as Notice & {service: Service}}
            />
        );
    }

    return (
        <div
            ref={slotRef}
            className='vntv-account-notices'

            // Whether focus was on the notice as OK was pressed, to put it
            // somewhere when the notice goes.
            onClickCapture={() => {
                hadFocus.current = Boolean(slotRef.current?.contains(document.activeElement));
            }}
        >{notice}</div>
    );
};
