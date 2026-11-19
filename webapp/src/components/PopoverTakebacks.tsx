import React from 'react';

import {fetchTakebacks, removeMySounds} from '../client';
import {useIdentity} from '../identity';
import {askOwn} from '../ownAnswers';
import type {Takebacks} from '../types';

// Your own profile popover's actions (design/3.2.0.md 4.6), through
// registerPopoverUserActionsComponent, which Mattermost draws in one's own
// popover too, after its own row (M16). Taking things back is for anybody
// signed in, linked or not, so somebody who has unlinked can still delete the
// sounds they added: the soundboard is for linked readers only, and this is
// open precisely for the others. Outside the panel, so the lock screen gains
// nothing. Until 3.2.1 it also offered not being named as anybody's biggest
// fan; there is no opt-out any more (design/3.2.1.md 2).
//
// Read when the popover opens (GET /takebacks, open, answered from memory by
// the server, so a Markdown image pointing at it costs nothing). Deleting
// needs a second press, which says in words what it does, and says how it
// went under its own button.
//
// Draws nothing in anybody else's popover, and nothing at all while there is
// nothing to take back.
export const PopoverTakebacks: React.FC<{user?: {id?: string}}> = ({user}) => {
    const me = useIdentity();
    const [state, setState] = React.useState<Takebacks | null>(null);
    const [armed, setArmed] = React.useState(false);
    const [soundsSaid, setSoundsSaid] = React.useState('');

    const own = Boolean(me && user?.id && user.id === me.userId);

    React.useEffect(() => {
        if (!own) {
            return undefined;
        }

        let current = true;
        askOwn(fetchTakebacks).then((answer) => {
            if (current && answer) {
                setState(answer);
            }
        }).catch(() => undefined);

        return () => {
            current = false;
        };
    }, [own]);

    if (!own || !me || !state) {
        return null;
    }

    const sounds = state.sounds;

    if (sounds === 0 && soundsSaid === '') {
        return null;
    }

    const deleteSounds = () => {
        setSoundsSaid('');
        if (!armed) {
            setArmed(true);

            return;
        }
        setArmed(false);
        removeMySounds().then((answer) => {
            setState((current) => (current ? {...current, sounds: 0} : current));
            setSoundsSaid(answer.deleted > 0 ? (answer.deleted === 1 ? 'Deleted your sound.' : `Deleted your ${answer.deleted} sounds.`) : 'You have no sounds on the board.');
        }).catch(() => setSoundsSaid('Couldn’t do that just now. Try again.'));
    };

    return (
        <div className='vntv vntv-inline vntv-takebacks'>
            <div className='vntv-takeback-slot'>
                {sounds > 0 ? (
                    <button
                        type='button'
                        className='vntv-takeback'
                        data-armed={armed ? 'true' : undefined}
                        onClick={deleteSounds}
                    >{armed ? `Click again to delete all ${sounds}` : 'Delete my sounds'}</button>
                ) : null}

                <p
                    className='vntv-takeback-said'
                    role='status'
                    data-tone={sounds === 0 ? 'done' : undefined}
                >{soundsSaid}</p>
            </div>
        </div>
    );
};
