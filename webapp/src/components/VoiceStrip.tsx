import React from 'react';

import {useIdentity} from '../identity';
import type {VoicePerson} from '../types';

import {HeadphonesIcon} from './icons';

type Props = {
    people?: VoicePerson[];
    connected?: boolean;
};

// Who is in voice, as one row at the top of the panel.
//
// It renders NOTHING when nobody is in there — the same rule the Up next rail
// follows, and the reason this is a row rather than a section: on the evenings
// when voice is empty it costs nothing at all, and on the evenings it is not,
// the names are already on screen without anybody opening anything.
//
// There is one voice channel, so there is no tree to draw and no popout worth
// having: the roster is a flat list of names, and a list of names is a line of
// text.
export const VoiceStrip: React.FC<Props> = ({people, connected}) => {
    const me = useIdentity();

    // Not connected is not the same as nobody being in there, and the panel
    // says neither: it simply does not claim to know. An admin is told what
    // went wrong through /api/v1/me, where nobody else can see it.
    if (!connected || !people || people.length === 0) {
        return null;
    }

    return (
        <div className='vntv-voice'>
            <span
                className='vntv-voice-icon'
                aria-hidden={true}
            ><HeadphonesIcon/></span>
            <ul
                className='vntv-voice-list'
                aria-label='In voice'
            >
                {people.map((person) => (
                    <li
                        key={person.name}
                        className='vntv-voice-person'
                        data-me={Boolean(me?.userId) && person.userId === me?.userId}
                        data-muted={Boolean(person.muted)}
                        title={person.muted ? `${person.name} — muted` : person.name}
                    >{person.name}</li>
                ))}
            </ul>
        </div>
    );
};
