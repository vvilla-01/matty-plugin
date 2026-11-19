import React from 'react';

import {adminBlockSounds, adminCheckSounds, adminRemoveSound, adminRestartSounds, fetchAdminSoundAudio, fetchAdminSounds, SoundRequestError} from '../client';
import {PRODUCT_NAME} from '../name';
import {seconds} from '../soundLogic';
import {canHearSounds, playStored} from '../soundEncode';
import type {Playing} from '../soundEncode';
import type {AdminSounds as Sounds} from '../types';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm, useCardFocus} from './adminParts';
import {SoundEmojiImage} from './EmojiChooser';

// System Console > Plugins > Matty > Soundboard > Sounds on the server (design/3.2.0.md 7.11):
// whether the plugin's Mumble account can play, from a fresh check of its own
// session, with Check again; every sound on the board, with Listen and Delete;
// who is blocked from the soundboard; and, when the list of sounds cannot be
// read, Start the board again. Admin routes only (under /api/v1/admin, system
// admins first), so it works without a token, whatever the soundboard's
// switch says. Nothing here waits for the page's Save button.

type Props = {
    id?: string;
    label?: string;
    disabled?: boolean;
};

const kilobytes = (bytes: number): string => `${Math.max(1, Math.round(bytes / 1024))} KB`;

function errorText(reason: unknown): string {
    if (reason instanceof SoundRequestError) {
        if (reason.code === 'no_such_user') {
            return 'Nobody here has that username.';
        }
        if (reason.code === 'full') {
            return 'Fifty people are blocked already, the most. Unblock somebody first.';
        }

        return reason.serverText || reason.message;
    }

    return reason instanceof Error ? reason.message : String(reason);
}

export const AdminSounds: React.FC<Props> = ({id, label, disabled}) => {
    const [list, setList] = React.useState<Sounds | null>(null);
    const [loadError, setLoadError] = React.useState('');
    const [busy, setBusy] = React.useState('');
    const [note, setNote] = React.useState('');
    const [error, setError] = React.useState('');
    const [name, setName] = React.useState('');
    const [hearable, setHearable] = React.useState(false);
    const [listening, setListening] = React.useState('');
    const playing = React.useRef<Playing | null>(null);

    // Which Listen is the newest: a press, Stop, or leaving the page moves it
    // on, so a sound still being fetched or decoded for an older one is
    // never started, or is stopped the moment it is.
    const listenTurn = React.useRef(0);
    const {statusRef, focus} = useCardFocus();
    const confirm = useCardConfirm(() => {
        setNote('');
        setError('');
    });

    React.useEffect(() => {
        void canHearSounds().then(setHearable);

        return () => {
            listenTurn.current += 1;
            playing.current?.stop();
            playing.current = null;
        };
    }, []);

    // Only the newest request's answer is used: one after an action and one
    // the admin asked for can cross.
    const latest = React.useRef(0);
    const load = React.useCallback(() => {
        const ticket = ++latest.current;
        fetchAdminSounds().then((next) => {
            if (ticket === latest.current) {
                setList(next);
                setLoadError('');
            }
        }).catch((reason: unknown) => {
            if (ticket === latest.current) {
                setLoadError(errorText(reason));
            }
        });
    }, []);

    React.useEffect(() => {
        load();

        return () => {
            latest.current++;
        };
    }, [load]);

    const act = (what: string, work: () => Promise<unknown>, done: string) => {
        setBusy(what);
        setNote('');
        setError('');
        confirm.disarm();

        work().then(() => setNote(done)).catch((reason: unknown) => setError(errorText(reason))).finally(() => {
            setBusy('');
            load();
        });
    };

    // A fresh check of the plugin's own Mumble session, said in the status line.
    const check = () => {
        setBusy('check');
        setNote('');
        setError('');
        adminCheckSounds().then((answer) => {
            setNote(answer.line || (answer.ok ? `${PRODUCT_NAME} can play sounds into Mumble.` : ''));
        }).catch((reason: unknown) => setError(errorText(reason))).finally(() => {
            setBusy('');
            load();
        });
    };

    // Listen, in this browser only, decoded where WebCodecs decodes.
    const listen = (soundId: string) => {
        listenTurn.current += 1;
        const turn = listenTurn.current;
        playing.current?.stop();
        playing.current = null;
        if (listening === soundId) {
            setListening('');

            return;
        }

        setListening(soundId);
        let started: Playing | null = null;
        fetchAdminSoundAudio(soundId).then((bytes) => (turn === listenTurn.current ? playStored(bytes, () => {
            if (started !== null && playing.current === started) {
                playing.current = null;
                setListening('');
            }
        }) : null)).then((sound) => {
            if (!sound) {
                return;
            }
            if (turn !== listenTurn.current) {
                sound.stop();

                return;
            }
            started = sound;
            playing.current = sound;
        }).catch((reason: unknown) => {
            if (turn === listenTurn.current) {
                setListening('');
                setError(errorText(reason));
            }
        });
    };

    const block = (username: string, blocked: boolean, done: string) => {
        const clean = username.trim().replace(/^@/, '');
        if (clean === '') {
            setError('Type the username of whoever to block.');

            return;
        }
        act(blocked ? 'block' : `unblock:${clean}`, () => adminBlockSounds(clean, blocked), done);
    };

    if (list === null) {
        return (
            <AdminCard
                id={id}
                label={label}
                focus={focus}
            >
                <AdminHead instant={true}>{loadError === '' ? 'Loading…' : 'The sounds could not be loaded.'}</AdminHead>
                {loadError === '' ? null : (
                    <div className='vntv-admin-actions'>
                        <AdminPill onPress={load}>{'Refresh'}</AdminPill>
                    </div>
                )}
                <AdminStatus
                    ref={statusRef}
                    note=''
                    error={loadError}
                />
            </AdminCard>
        );
    }

    const waiting = busy !== '';
    const {totals} = list;

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead instant={true}>
                {list.bot.line || (list.bot.ok ? `${PRODUCT_NAME} can play sounds into Mumble.` : `${PRODUCT_NAME} can’t play sounds into Mumble just now.`)}
            </AdminHead>

            {!list.on ? (
                <p className='vntv-admin-line vntv-admin-quiet'>
                    {'The soundboard is switched off: nobody can add or play sounds, and the sounds below are kept.'}
                </p>
            ) : null}

            {!list.readable ? (
                <div
                    className='vntv-admin-callout'
                    data-tone='error'
                >
                    <p className='vntv-admin-line'>
                        {'The list of sounds couldn’t be read. A copy is kept in the plugin’s store as quarantine_sounds.'}
                    </p>
                </div>
            ) : (
                <p className='vntv-admin-line'>
                    {totals.sounds === 0 ? 'No sounds on the board.' : `${totals.sounds} ${totals.sounds === 1 ? 'sound' : 'sounds'}, ${kilobytes(totals.bytes)}, ${totals.people} ${totals.people === 1 ? 'person' : 'people'}.`}
                </p>
            )}

            {list.sounds.length > 0 ? (
                <ul className='vntv-admin-list'>
                    {list.sounds.map((sound) => {
                        const removeId = `remove:${sound.id}`;

                        return (
                            <li
                                key={sound.id}
                                className='vntv-admin-row'
                                data-align='top'
                            >
                                <span
                                    className='vntv-admin-sound-emoji'
                                    aria-hidden={true}
                                >
                                    <SoundEmojiImage emoji={sound.emoji}/>
                                </span>
                                <div className='vntv-admin-row-main'>
                                    <p className='vntv-admin-row-title'>
                                        <span className='vntv-admin-row-name'>{sound.name}</span>
                                        {sound.ok ? null : (
                                            <span
                                                className='vntv-admin-sound-bad'
                                                title='Its audio is missing or does not match its checksum: it cannot be played.'
                                            >{'⚠ audio missing or damaged'}</span>
                                        )}
                                    </p>
                                    <p className='vntv-admin-row-meta'>
                                        {`Added by ${sound.owner} · ${new Date(sound.at).toLocaleDateString()} · ${seconds(sound.ms)} · ${kilobytes(sound.bytes)} · ${sound.level.toFixed(1)} dB`}
                                    </p>
                                    <div
                                        className='vntv-admin-actions'
                                        role='group'
                                        aria-label={sound.name}
                                    >
                                        {hearable && sound.ok ? (
                                            <AdminPill
                                                waiting={waiting}
                                                pressed={listening === sound.id}
                                                label={`${listening === sound.id ? 'Stop' : 'Listen to'} ${sound.name}`}
                                                onPress={() => listen(sound.id)}
                                            >{listening === sound.id ? 'Stop' : 'Listen'}</AdminPill>
                                        ) : null}
                                        <AdminPill
                                            off={disabled}
                                            waiting={waiting}
                                            working={busy === removeId}
                                            workingWords='Deleting…'
                                            confirms={true}
                                            armed={confirm.armed === removeId}
                                            label={`Delete ${sound.name}`}
                                            onPress={(event) => {
                                                if (confirm.press(removeId, `Click again to delete ${sound.name} from the board.`, event)) {
                                                    act(removeId, () => adminRemoveSound(sound.id), `Deleted ${sound.name}.`);
                                                }
                                            }}
                                        >{'Delete'}</AdminPill>
                                    </div>
                                </div>
                            </li>
                        );
                    })}
                </ul>
            ) : null}

            <h4 className='vntv-admin-sub'>{'Blocked from the soundboard'}</h4>
            <p className='vntv-admin-line vntv-admin-quiet'>{'Blocking stops somebody adding and playing sounds. Their sounds stay until they are deleted.'}</p>
            <div className='vntv-access-entry'>
                <label
                    className='vntv-sr-only'
                    htmlFor={`${id || 'soundsadmin'}-block`}
                >{'Username to block from the soundboard'}</label>
                <input
                    id={`${id || 'soundsadmin'}-block`}
                    className='vntv-input'
                    type='text'
                    placeholder='username'
                    autoComplete='off'
                    spellCheck={false}
                    value={name}
                    disabled={disabled}
                    onChange={(event) => setName(event.target.value)}
                    onKeyDown={(event) => {
                        if (event.key === 'Enter') {
                            event.preventDefault();
                            block(name, true, `Blocked @${name.trim().replace(/^@/, '')} from the soundboard.`);
                            setName('');
                        }
                    }}
                />
                <AdminPill
                    off={disabled}
                    waiting={waiting}
                    working={busy === 'block'}
                    workingWords='Blocking…'
                    onPress={() => {
                        block(name, true, `Blocked @${name.trim().replace(/^@/, '')} from the soundboard.`);
                        setName('');
                    }}
                >{'Block'}</AdminPill>
            </div>
            {list.blocked.length === 0 ? (
                <p className='vntv-admin-line vntv-admin-quiet'>{'Nobody is blocked.'}</p>
            ) : (
                <ul className='vntv-admin-list'>
                    {list.blocked.map((person) => (
                        <li
                            key={person.userId}
                            className='vntv-admin-row'
                        >
                            <span className='vntv-admin-row-text'>
                                {`${person.name || `@${person.username}`}${person.username ? ` (@${person.username})` : ''} · by ${person.by} · since ${new Date(person.at).toLocaleDateString()}`}
                            </span>
                            <AdminPill
                                off={disabled}
                                waiting={waiting}
                                working={busy === `unblock:${person.username}`}
                                workingWords='Unblocking…'
                                label={`Unblock ${person.name || person.username}`}
                                onPress={() => block(person.username, false, `${person.name || `@${person.username}`} can use the soundboard again.`)}
                            >{'Unblock'}</AdminPill>
                        </li>
                    ))}
                </ul>
            )}

            <div className='vntv-admin-actions'>
                <AdminPill
                    waiting={waiting}
                    working={busy === 'check'}
                    workingWords='Checking…'
                    title={`Ask ${PRODUCT_NAME}’s Mumble session again whether it can play`}
                    onPress={check}
                >{'Check again'}</AdminPill>
                {!list.readable ? (
                    <AdminPill
                        tone='danger'
                        off={disabled}
                        waiting={waiting}
                        working={busy === 'restart'}
                        workingWords='Starting…'
                        confirms={true}
                        armed={confirm.armed === 'restart'}
                        onPress={(event) => {
                            if (confirm.press('restart', 'Click again to start the board again, empty. The old sounds’ audio is deleted when the plugin next starts, which leaves time to put the list back by hand.', event)) {
                                act('restart', adminRestartSounds, 'The board is empty and working again.');
                            }
                        }}
                    >{'Start the board again'}</AdminPill>
                ) : null}
                <AdminPill
                    waiting={waiting}
                    onPress={load}
                >{'Refresh'}</AdminPill>
            </div>

            <AdminStatus
                ref={statusRef}
                note={confirm.hint || note}
                error={error}
            />
        </AdminCard>
    );
};
