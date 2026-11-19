import React from 'react';

import {useIdentity} from '../identity';
import type {Voice, VoicePerson} from '../types';
import {voiceGroups, voiceMark, voicePersonLabel} from '../voice';

import {HeadphonesIcon, HeadphonesOffIcon, MicOffIcon} from './icons';

type Props = {
    voice?: Voice;
};

// Who is in voice, at the top of the panel: each voice channel somebody is
// in, in the voice server's own order, with the channel's name over the
// people in it. Channels nobody is in are left out.
//
// It renders NOTHING when nobody is in there — the same rule the Up next rail
// follows: on the evenings when voice is empty it costs nothing at all, and on
// the evenings it is not, the names are already on screen without anybody
// opening anything. A server before 4.3.0 sends no channels, and its people
// are one line of names with no channel over them.
export const VoiceStrip: React.FC<Props> = ({voice}) => {
    const me = useIdentity();

    // Not connected is not the same as nobody being in there, and the panel
    // says neither: it simply does not claim to know. An admin is told what
    // went wrong through /api/v1/me, where nobody else can see it.
    const groups = voiceGroups(voice);
    if (groups.length === 0) {
        return null;
    }

    const person = (who: VoicePerson) => {
        const mark = voiceMark(who);

        return (
            <li
                key={who.session === undefined ? who.name : `${who.session}`}
                className='vntv-voice-person'
                data-me={Boolean(me?.userId) && who.userId === me?.userId}
                data-muted={Boolean(who.muted)}
                title={voicePersonLabel(who)}
            >
                {who.name}
                {mark !== '' && (
                    <span
                        className='vntv-voice-mark'
                        aria-hidden={true}
                    >{mark === 'deaf' ? <HeadphonesOffIcon/> : <MicOffIcon/>}</span>
                )}
                {mark !== '' && <span className='vntv-sr-only'>{mark === 'deaf' ? ', deafened' : ', muted'}</span>}
            </li>
        );
    };

    const flat = groups.length === 1 && !groups[0].channel;

    return (
        <div
            className='vntv-voice'
            data-channels={!flat}
        >
            <span
                className='vntv-voice-icon'
                aria-hidden={true}
            ><HeadphonesIcon/></span>
            {flat ? (
                <ul
                    className='vntv-voice-list'
                    aria-label='In voice'
                >
                    {groups[0].people.map(person)}
                </ul>
            ) : (
                <ul
                    className='vntv-voice-channels'
                    aria-label='In voice'
                >
                    {groups.map((group) => (
                        <li
                            key={group.channel ? `c${group.channel.id}` : 'elsewhere'}
                            className='vntv-voice-channel'
                        >
                            {group.channel && <span className='vntv-voice-channel-name'>{group.channel.name}</span>}
                            <ul
                                className='vntv-voice-list'
                                aria-label={group.channel ? `In ${group.channel.name}` : 'In voice'}
                            >
                                {group.people.map(person)}
                            </ul>
                        </li>
                    ))}
                </ul>
            )}
        </div>
    );
};
