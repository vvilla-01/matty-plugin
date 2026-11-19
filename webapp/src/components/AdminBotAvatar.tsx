import React from 'react';

import {adoptBot, fetchBotIdentity, uploadBotAvatar} from '../client';
import type {BotIdentity} from '../client';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm, useCardFocus} from './adminParts';
import type {AdminCardProps} from './adminParts';
import {ImageCropper} from './ImageCropper';

// Mattermost's own limit is larger; the plugin's endpoint takes up to 2 MB,
// and the cropped square is far under it.
const MAX_BYTES = 2 * 1024 * 1024;

// The picture the bot posts under, changed from System Console.
//
// Uploaded to Mattermost's own user-image store rather than kept in the
// plugin's configuration: a few hundred kilobytes of base64 in a config value
// would be written to disk, held in memory and shipped to every node on every
// configuration change, for a picture.
//
// It therefore saves IMMEDIATELY, not with the page's Save button, and says so
// in its head — a control that has already done its work while the Save button
// still looks unpressed is otherwise just confusing.
export const AdminBotAvatar: React.FC<AdminCardProps> = ({id, label, disabled}) => {
    const [bot, setBot] = React.useState<BotIdentity | null>(null);
    const [loaded, setLoaded] = React.useState(false);
    const [loadError, setLoadError] = React.useState('');
    const [stamp, setStamp] = React.useState(() => Date.now());
    const [busy, setBusy] = React.useState(false);
    const [note, setNote] = React.useState('');
    const [error, setError] = React.useState('');
    const [cropping, setCropping] = React.useState<File | null>(null);
    const picker = React.useRef<HTMLInputElement>(null);
    const chooser = React.useRef<HTMLButtonElement>(null);

    // Back to Choose a file once the cropper closes, by Cancel or by Use
    // this, rather than to the top of the page: before the card's own focus
    // home below, so that never sees focus with nowhere to go.
    const wasCropping = React.useRef(false);
    React.useLayoutEffect(() => {
        if (cropping !== null) {
            wasCropping.current = true;

            return;
        }
        if (wasCropping.current) {
            wasCropping.current = false;
            const active = document.activeElement;
            if (!active || active === document.body) {
                chooser.current?.focus();
            }
        }
    }, [cropping]);

    const {statusRef, focus} = useCardFocus();
    const confirm = useCardConfirm(() => {
        setNote('');
        setError('');
    });

    // A picture that would not load shows the question mark rather than a
    // broken image; a new stamp tries again.
    const [broken, setBroken] = React.useState(false);
    React.useEffect(() => setBroken(false), [stamp, bot?.userId]);

    // A failed look keeps what it had, and says why.
    const load = React.useCallback(() => {
        fetchBotIdentity().then((next) => {
            setBot(next);
            setLoaded(true);
            setLoadError('');
        }).catch((reason: unknown) => {
            setLoaded(true);
            setLoadError(reason instanceof Error ? reason.message : String(reason));
        });
    }, []);

    React.useEffect(load, [load]);

    const takeOver = React.useCallback((userId: string) => {
        setBusy(true);
        setNote('');
        setError('');

        adoptBot(userId).then(() => {
            setNote('Done. The plugin now posts as this account, and the one it was using has been disabled — not deleted, so nothing it posted is lost.');
            setStamp(Date.now());
            load();
        }).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(false));
    }, [load]);

    // Choosing a file opens the cropper; the square picked there is what is
    // uploaded.
    const choose = React.useCallback((file?: File) => {
        if (!file) {
            return;
        }

        setNote('');
        setError('');
        setCropping(file);
    }, []);

    const save = React.useCallback((file: File) => {
        setBusy(true);
        setNote('');
        setError('');

        uploadBotAvatar(file).then(() => {
            setCropping(null);
            setNote('Saved. The bot posts under the new picture from now on.');

            // Mattermost serves avatars with a long cache life, so the preview
            // has to ask for a different URL to see the new one.
            setStamp(Date.now());
        }).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(false));
    }, []);

    let head: React.ReactNode = 'Loading…';
    if (bot) {
        head = (
            <>
                <b>{bot.displayName || bot.username}</b>
                {` @${bot.username}`}
            </>
        );
    } else if (loaded) {
        head = loadError === '' ? 'The bot account is not ready yet.' : 'The bot account could not be read.';
    }

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead instant={true}>{head}</AdminHead>

            <div className='vntv-avatar'>
                <span
                    className='vntv-admin-thumb'
                    data-shape='circle'
                >
                    {bot?.userId && !broken ? (
                        <img
                            className='vntv-admin-thumb-image'
                            src={`/api/v4/users/${bot.userId}/image?t=${stamp}`}
                            alt={`The current picture for @${bot.username}`}
                            onError={() => setBroken(true)}
                        />
                    ) : (
                        <span
                            className='vntv-admin-thumb-none'
                            aria-hidden={true}
                        >{'?'}</span>
                    )}
                </span>

                <div className='vntv-avatar-side'>
                    {bot?.description ? (
                        <p className='vntv-admin-line vntv-admin-quiet'>{bot.description}</p>
                    ) : null}
                    <div className='vntv-admin-actions'>
                        <AdminPill
                            buttonRef={chooser}
                            off={disabled || cropping !== null || !bot?.userId}
                            waiting={busy}
                            onPress={() => picker.current?.click()}
                        >{'Choose a file'}</AdminPill>
                    </div>
                </div>
            </div>

            <input
                ref={picker}
                type='file'
                accept='image/png,image/jpeg,image/gif,image/webp'
                className='vntv-admin-file'
                tabIndex={-1}
                onChange={(event) => {
                    choose(event.target.files?.[0]);

                    // Cleared so choosing the same file twice still fires.
                    event.target.value = '';
                }}
            />

            {cropping ? (
                <ImageCropper
                    file={cropping}
                    maxBytes={MAX_BYTES}
                    busy={busy}
                    onCancel={() => setCropping(null)}
                    onCrop={save}
                />
            ) : null}

            {/* The name on the ACCOUNT, which is not always the name in the
                Username box: Mattermost refuses a username somebody else
                already has, and an admin who typed one and saw nothing
                happen has no other way to find that out.

                When something is sitting on the name, say what it is and,
                if the plugin may, offer to use it. Typically this is the
                ORIGINAL bot, left behind when the plugin's id changed — and
                it is the one with the history, which is why the offer is to
                move onto it rather than to get it out of the way. */}
            {bot?.adoptable ? (
                <div className='vntv-admin-callout'>
                    <p className='vntv-admin-line'>
                        <b>{`@${bot.adoptable.username} already exists.`}</b>
                        {` It was made by ${bot.adoptable.owner}, which is not installed any more, `}
                        {'so nothing is using it. Everything it ever posted is still attached to it.'}
                    </p>
                    <p className='vntv-admin-line vntv-admin-quiet'>
                        {`Using it keeps that history under one name. @${bot.username} is then disabled, `}
                        {'not deleted: deleting an account in Mattermost deletes every post it made.'}
                    </p>
                    <div className='vntv-admin-actions'>
                        <AdminPill
                            off={disabled}
                            waiting={busy}
                            working={busy && cropping === null}
                            workingWords='Working…'
                            confirms={true}
                            armed={confirm.armed === 'adopt'}
                            onPress={(event) => {
                                const adoptable = bot.adoptable;
                                if (adoptable && confirm.press('adopt', `Click again to post as @${adoptable.username} from now on. @${bot.username} is disabled, not deleted.`, event)) {
                                    takeOver(adoptable.userId);
                                }
                            }}
                        >{`Use @${bot.adoptable.username}`}</AdminPill>
                    </div>
                </div>
            ) : bot?.error ? (
                <div className='vntv-admin-callout' data-tone='error'>
                    <p className='vntv-admin-line'>
                        {'The bot could not be renamed, so it kept the name it had: '}
                        {bot.error}
                        {bot.adoptBlocked ? ` ${bot.adoptBlocked}.` : ''}
                    </p>
                </div>
            ) : null}

            <AdminStatus
                ref={statusRef}
                note={confirm.hint || note}
                error={error || (loadError === '' || !bot ? loadError : `The bot account could not be read: ${loadError}`)}
            />
        </AdminCard>
    );
};
