import React from 'react';

import {latestAccess, refreshIdentity} from '../identity';

// Try again, where the panel could not tell the reader's access: the lock's
// NO SIGNAL face, and the panel's quiet notice once it had an answer before
// (design/lock-screen.md 4.3). One read of "me", the shared one, per press,
// and never by itself — a plugin that is restarting answers 404 to
// everything, and a steady stream of those from one browser is what the
// fail2ban jail in front of Mattermost looks for.
//
// It says what it is doing: "Checking…" while the read is out, and, if the
// answer is still that nobody knows, a line saying so beside it. A retry that
// failed again otherwise looked, and sounded, like a button that does nothing.
// When an answer does come, what holds this goes (the face changes, the
// notice goes), so there is nothing to say for that here.
//
// When it goes with focus on it, after a keyboard press, the browser drops
// that focus to the page's body, and from there Mattermost sends the next
// letter to its message box. So focus goes first to the root of the lock or
// of the panel around it, as it does when the words after Disconnect Twitch
// go (TwitchConnect.tsx): both are focusable, and both keep a typed key from
// that box. Only when the button still has focus as it goes: focus somebody
// has put somewhere else since stays there.

export const STILL_NOT_ANSWERING = 'Still not answering. Try again in a moment.';

export const TryAgain: React.FC<{className?: string}> = ({className}) => {
    const [checking, setChecking] = React.useState(false);
    const [said, setSaid] = React.useState({text: '', n: 0});

    const alive = React.useRef(true);
    React.useEffect(() => {
        alive.current = true;

        return () => {
            alive.current = false;
        };
    }, []);

    // A layout effect's cleanup runs while the button is still in the page,
    // where it can still be asked whether it has focus; a plain effect's
    // runs after it has gone, and focus with it.
    const keyRef = React.useRef<HTMLButtonElement | null>(null);
    React.useLayoutEffect(() => {
        const key = keyRef.current;

        return () => {
            if (key && document.activeElement === key) {
                key.closest<HTMLElement>('.vntv-panel')?.focus({preventScroll: true});
            }
        };
    }, []);

    const press = () => {
        if (checking) {
            return;
        }

        setChecking(true);
        setSaid((current) => ({text: '', n: current.n}));
        void refreshIdentity().then(() => {
            if (!alive.current) {
                return;
            }

            setChecking(false);
            if (latestAccess() === 'unknown') {
                setSaid((current) => ({text: STILL_NOT_ANSWERING, n: current.n + 1}));
            }
        });
    };

    return (
        <>
            {/* Not disabled while it asks: a disabled button drops the
                keyboard focus it had. */}
            <button
                ref={keyRef}
                type='button'
                className={className ? `vntv-pill vntv-tryagain ${className}` : 'vntv-pill vntv-tryagain'}
                aria-disabled={checking ? true : undefined}
                onClick={press}
            >{checking ? 'Checking…' : 'Try again'}</button>

            {/* Always in the page, so what is put in it is announced; the
                same words twice in a row are told apart by a no-break space,
                so they are said again. */}
            <p
                className='vntv-tryagain-said'
                role='status'
                aria-live='polite'
            >{said.text === '' ? '' : `${said.text}${said.n % 2 === 0 ? ' ' : ''}`}</p>
        </>
    );
};
