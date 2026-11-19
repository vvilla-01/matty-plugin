import React from 'react';

import {useFocusHome} from '../focusHome';
import type {FocusHome} from '../focusHome';
import {useTwoClicks} from '../twoClicks';

// The pieces every System Console card is made of, so the cards read as
// one set (styles.ts, "The System Console").

// What System Console hands every custom setting, of what a card uses: its
// configuration key (PluginSettings.Plugins.ceo+villa+matty.<key>, which
// the label beside the card points at) and its display name.
export type AdminCardProps = {
    id?: string;
    label?: string;
    disabled?: boolean;
};

type CardProps = {
    id?: string;
    label?: string;
    focus?: FocusHome;
    children?: React.ReactNode;
};

// The card itself. Named by its setting's label, so a screen reader moving
// into it hears which card it is in before five buttons that all say
// Refresh; and carrying the key Mattermost's label points at.
//
// ignore-marking: when an admin searches the System Console, Mattermost runs
// mark.js over the page, which wraps every match in a <mark> inside whatever
// element holds it (search_keyword_marking.tsx, which excludes
// '.ignore-marking *'). Inside a card that text belongs to React, and a node
// React no longer recognises is how a re-render ends in duplicated words or a
// crash.
export const AdminCard: React.FC<CardProps> = ({id, label, focus, children}) => (
    <div
        id={id}
        className='vntv vntv-admin ignore-marking'
        role='group'
        aria-label={label || undefined}
        onFocus={focus?.onFocus}
        onBlur={focus?.onBlur}
    >{children}</div>
);

// Where focus goes when the button that had it goes away — its row deleted,
// a block that takes the Block button with it, a button with nothing left to
// do — rather than to the top of the page (focusHome.ts): the card's status
// line, which by then says what was done. Pass focus to AdminCard and
// statusRef to AdminStatus.
export function useCardFocus(): {statusRef: React.MutableRefObject<HTMLParagraphElement | null>; focus: FocusHome} {
    const statusRef = React.useRef<HTMLParagraphElement | null>(null);
    const focus = useFocusHome(() => statusRef.current);

    return {statusRef, focus};
}

// The head of a card: what it holds, in one line, and — for a card that acts
// at once rather than with the page's Save button — a tag that says so. Said
// once here, rather than in a sentence on every card.
export const AdminHead: React.FC<{instant?: boolean; children?: React.ReactNode}> = ({instant, children}) => (
    <div className='vntv-admin-head'>
        <p className='vntv-admin-stat'>{children}</p>
        {instant ? (
            <span
                className='vntv-admin-tag'
                title='Takes effect as soon as you press it; the page’s Save button is not needed.'
            >{'No Save needed'}</span>
        ) : null}
    </div>
);

type StatusProps = {
    note: string;
    error: string;
};

// The two lines that are always there: what was just done (or, while a pill
// waits for its second press, what that press will do), and what went wrong.
// Always in the page, empty or not, so what is put in them is announced;
// empty, they take no room. The first is where focus goes when the button
// that had it goes away: focusable from script, never from Tab.
export const AdminStatus = React.forwardRef<HTMLParagraphElement, StatusProps>(({note, error}, ref) => (
    <>
        <p
            ref={ref}
            className='vntv-admin-status'
            role='status'
            tabIndex={-1}
        >{note}</p>
        <p
            className='vntv-admin-status'
            data-tone='error'
            role='alert'
        >{error}</p>
    </>
));

AdminStatus.displayName = 'AdminStatus';

type PillProps = {
    // The words at rest. Pressed once when it confirms, it says them again
    // with a question mark.
    children: string;
    onPress: (event: React.MouseEvent<HTMLButtonElement>) => void;

    // Nothing for it to do, or a page this admin may only read: a real
    // disabled — except while it has focus. Chrome moves focus off a control
    // that has just been disabled a moment later, to the page's body, firing
    // no blur, so neither the render that disabled it nor focusHome.ts can
    // see it go; a keyboard admin who has just pressed it would be thrown to
    // the top of the page. So a pill that is turned off while focused is only
    // aria-disabled until focus leaves it.
    off?: boolean;

    // Something on the card is working, this pill's own press included: it
    // looks unavailable and ignores presses, but stays focusable
    // (aria-disabled), so a keyboard admin keeps their place.
    waiting?: boolean;

    // This pill's own work, and what it says while it is done.
    working?: boolean;
    workingWords?: string;

    // Acts only on a second press (twoClicks.ts), and whether that press is
    // being waited for now.
    confirms?: boolean;
    armed?: boolean;

    // primary: the card's main action. danger: one that deletes what cannot
    // be put back, such as Delete all Twitch data, in the console's red.
    tone?: 'primary' | 'danger';
    title?: string;
    label?: string;

    // The id of words that explain what pressing it does, said after its
    // name: Unlink's, which are always on the card under its heading.
    describedBy?: string;
    pressed?: boolean;
    buttonRef?: React.Ref<HTMLButtonElement>;
};

// One pill. Its words at rest, armed and working are laid over each other in
// one grid cell, so it is as wide as the longest of them all the time: the
// second click lands where the first did, and nothing beside it moves. The
// faces not shown are visibility: hidden, which also leaves them out of its
// accessible name.
export const AdminPill: React.FC<PillProps> = ({children, onPress, off, waiting, working, workingWords, confirms, armed, tone, title, label, describedBy, pressed, buttonRef}) => {
    const [focused, setFocused] = React.useState(false);
    const disabled = Boolean(off) && !focused;
    const inert = Boolean(waiting || working || off);
    let shown: 'rest' | 'armed' | 'working' = 'rest';
    if (working && workingWords) {
        shown = 'working';
    } else if (confirms && armed && !inert) {
        shown = 'armed';
    }

    return (
        <button
            ref={buttonRef}
            type='button'
            className='vntv-pill'
            disabled={disabled}
            aria-disabled={!disabled && inert ? true : undefined}
            aria-pressed={pressed}
            aria-label={label}
            aria-describedby={describedBy}
            title={title}
            data-tone={tone}
            data-armed={shown === 'armed' ? 'true' : undefined}
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
            onClick={(event) => {
                if (!inert) {
                    onPress(event);
                }
            }}
        >
            <span className='vntv-pill-faces'>
                <span
                    className='vntv-pill-face'
                    data-shown={shown === 'rest' ? 'true' : undefined}
                >{children}</span>
                {confirms ? (
                    <span
                        className='vntv-pill-face'
                        data-shown={shown === 'armed' ? 'true' : undefined}
                    >{`${children}?`}</span>
                ) : null}
                {workingWords ? (
                    <span
                        className='vntv-pill-face'
                        data-shown={shown === 'working' ? 'true' : undefined}
                    >{workingWords}</span>
                ) : null}
            </span>
        </button>
    );
};

// The second press, for a card: which pill waits for it, and what that press
// will do, said in the card's status line while it waits. press() is called
// from the pill's onPress with the pill's name and that sentence; it is true
// on the press that should act.
export type CardConfirm = {
    armed: string;
    hint: string;
    row: string;
    press: (id: string, hint: string, event: {detail: number}, row?: string) => boolean;
    disarm: () => void;
};

export function useCardConfirm(onArm?: () => void): CardConfirm {
    const twice = useTwoClicks();
    const [waiting, setWaiting] = React.useState({id: '', hint: '', row: ''});
    const armOf = React.useRef(onArm);
    armOf.current = onArm;

    const press = React.useCallback((id: string, hint: string, event: {detail: number}, row = '') => {
        const arming = twice.armed !== id;
        if (twice.press(id, event)) {
            return true;
        }
        if (arming) {
            setWaiting({id, hint, row});
            armOf.current?.();
        }

        return false;
    }, [twice]);

    const current = twice.armed !== '' && twice.armed === waiting.id;

    return {
        armed: twice.armed,
        hint: current ? waiting.hint : '',
        row: current ? waiting.row : '',
        press,
        disarm: twice.disarm,
    };
}
