import React from 'react';

import {adminChannelPictureURL, fetchChannelPictures, resetChannelPicture, uploadChannelPicture} from '../client';
import type {AdminChannel, ChannelPictureKind} from '../client';
import {PLUGIN_ID} from '../manifest';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm, useCardFocus} from './adminParts';
import {ImageCropper} from './ImageCropper';

// The server's limit, which applies to the cropped picture that is sent.
const MAX_BYTES = 512 * 1024;

// Two pictures for each 24/7 channel, changed from System Console.
//
// A person's stream wears their Mattermost profile picture, and its Live now
// card shows a still of what is on. An always-on channel belongs to nobody and
// has no still, so it has these instead: a square PICTURE, where a face would
// be, and a wide CARD picture, filling its Live now card. Without a card
// picture the card shows the square one like an album cover, on a blur of
// itself.
//
// One row per key in 24/7 stream keys — as SAVED: a key typed into that
// box appears here once the page has been saved. System Console hands every
// custom setting the saved configuration and does NOT remount it on Save, so
// the list is reloaded when the saved keys change rather than only on mount.
//
// Choosing a file does not upload it: it opens the cropper, where the part of
// the photo to use is picked, and that is what is sent. Saved immediately once
// it is, like the artwork, and never touching the page's Save button: the
// picture goes to the plugin's own store, not into the configuration.
type Props = {
    id?: string;
    label?: string;
    disabled?: boolean;

    // The saved configuration, as System Console passes it to every custom
    // setting. Plugin setting keys arrive lowercased.
    config?: {PluginSettings?: {Plugins?: Record<string, Record<string, unknown> | undefined>}};
};

type Cropping = {key: string; kind: ChannelPictureKind; file: File};

const slotId = (key: string, kind: ChannelPictureKind): string => `${kind}:${key}`;

export const AdminChannelPictures: React.FC<Props> = ({id, label, disabled, config}) => {
    const [channels, setChannels] = React.useState<AdminChannel[] | null>(null);
    const [loadError, setLoadError] = React.useState('');
    const [busy, setBusy] = React.useState('');
    const [note, setNote] = React.useState('');
    const [error, setError] = React.useState('');

    const savedKeys = String(config?.PluginSettings?.Plugins?.[PLUGIN_ID]?.alwaysonstreamkeys ?? '');
    const [cropping, setCropping] = React.useState<Cropping | null>(null);
    const pickers = React.useRef<Record<string, HTMLInputElement | null>>({});
    const choosers = React.useRef<Record<string, HTMLButtonElement | null>>({});

    // Back to the slot's Choose a file once its cropper closes, by Cancel or
    // by Use this, rather than to the top of the page. Before the card's own
    // focus home below, so that never sees focus with nowhere to go.
    const cropped = React.useRef('');
    React.useLayoutEffect(() => {
        if (cropping !== null) {
            cropped.current = slotId(cropping.key, cropping.kind);

            return;
        }
        const slot = cropped.current;
        cropped.current = '';
        const active = document.activeElement;
        if (slot !== '' && (!active || active === document.body)) {
            choosers.current[slot]?.focus();
        }
    }, [cropping]);

    const {statusRef, focus} = useCardFocus();

    // Removing an uploaded picture deletes it, so it takes a second press.
    const confirm = useCardConfirm(() => {
        setNote('');
        setError('');
    });

    // A failed reload keeps the rows it had. Emptying them would put up "there
    // are no 24/7 stream keys", which is not what went wrong.
    const load = React.useCallback(() => {
        fetchChannelPictures().then((next) => {
            setChannels(next);
            setLoadError('');

            // A crop open for a key that has just been taken out of the
            // setting goes with its row — and with it the only Cancel, which
            // would otherwise leave every other row's buttons disabled.
            setCropping((open) => (open && !next.some((channel) => channel.key === open.key) ? null : open));
        }).catch((reason: unknown) => {
            setLoadError(reason instanceof Error ? reason.message : String(reason));
        });
    }, []);

    React.useEffect(load, [load, savedKeys]);

    const choose = React.useCallback((key: string, kind: ChannelPictureKind, file?: File) => {
        if (!file) {
            return;
        }

        setNote('');
        setError('');
        setCropping({key, kind, file});
    }, []);

    const save = React.useCallback((key: string, handle: string, kind: ChannelPictureKind, file: File) => {
        setBusy(slotId(key, kind));
        setNote('');
        setError('');

        uploadChannelPicture(handle, file, kind).then(() => {
            setCropping(null);
            setNote('Saved. Everybody’s panel picks it up within a poll.');
            load();
        }).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(''));
    }, [load]);

    const revert = React.useCallback((key: string, handle: string, kind: ChannelPictureKind) => {
        setBusy(slotId(key, kind));
        setNote('');
        setError('');

        resetChannelPicture(handle, kind).then(() => {
            setNote('Done. Everybody’s panel picks it up within a poll.');
            load();
        }).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(''));
    }, [load]);

    if (channels === null) {
        return (
            <AdminCard
                id={id}
                label={label}
                focus={focus}
            >
                <AdminHead instant={true}>{loadError === '' ? 'Loading…' : 'The 24/7 channels could not be loaded.'}</AdminHead>
                <AdminStatus
                    ref={statusRef}
                    note=''
                    error={loadError}
                />
            </AdminCard>
        );
    }

    // Read-only, or another slot's cropper open: nothing to press. Working:
    // pressable again in a moment, so focus stays where it is.
    const off = disabled || cropping !== null;
    const waiting = busy !== '';

    // One of a channel's two pictures: its preview, what it is, and the
    // controls to change it.
    const slot = (channel: AdminChannel, kind: ChannelPictureKind) => {
        const label = channel.name || channel.key;
        const wide = kind === 'thumb';
        const version = wide ? channel.thumbVersion : channel.version;
        const custom = wide ? Boolean(channel.thumbCustom) : channel.custom;
        const slot = slotId(channel.key, kind);

        // What is shown now, when nothing has been uploaded.
        const fallback = (() => {
            if (custom) {
                return '';
            }
            if (wide) {
                return channel.version ?
                    ' None yet: the card shows the picture as an album cover, or a linked account’s picture.' :
                    ' None yet, and no picture either: the card shows a plain wash of colour.';
            }

            return ' None yet: it shows its initials.';
        })();

        return (
            <li
                className='vntv-admin-row'
                data-align='top'
            >
                {/* Both pictures in a slot as wide as the card picture, so the
                    words beside them start in one column. */}
                <span className='vntv-admin-thumbslot'>
                    <span
                        className='vntv-admin-thumb'
                        data-shape={wide ? 'wide' : 'circle'}
                    >
                        {version ? (
                            <img
                                className='vntv-admin-thumb-image'
                                src={adminChannelPictureURL(channel.handle, version, kind)}
                                alt={wide ? `The card picture for ${label}` : `The picture for ${label}`}
                            />
                        ) : (
                            <span
                                className='vntv-admin-thumb-none'
                                aria-hidden={true}
                            >{'?'}</span>
                        )}
                    </span>
                </span>

                <div className='vntv-admin-row-main'>
                    <p className='vntv-admin-row-title'>
                        <span className='vntv-admin-row-name'>{wide ? 'Card picture' : 'Picture'}</span>
                        {custom ? (
                            <span
                                className='vntv-admin-flag'
                                data-tone='accent'
                            >{'Custom'}</span>
                        ) : null}
                    </p>
                    <p className='vntv-admin-row-meta'>
                        {wide ? 'Fills its Live now card, saved at 960 × 540.' : 'In place of a profile picture, saved at 512 × 512.'}
                        {fallback}
                    </p>

                    {cropping?.key === channel.key && cropping.kind === kind ? (
                        <ImageCropper
                            file={cropping.file}
                            maxBytes={MAX_BYTES}
                            shape={wide ? 'wide' : 'circle'}
                            busy={busy === slot}
                            onCancel={() => setCropping(null)}
                            onCrop={(cropped) => save(channel.key, channel.handle, kind, cropped)}
                        />
                    ) : (
                        <div
                            className='vntv-admin-actions'
                            role='group'
                            aria-label={`${wide ? 'Card picture' : 'Picture'} for ${label}`}
                        >
                            <AdminPill
                                buttonRef={(node) => {
                                    choosers.current[slot] = node;
                                }}
                                off={off}
                                waiting={waiting}
                                onPress={() => pickers.current[slot]?.click()}
                            >{'Choose a file'}</AdminPill>

                            <AdminPill
                                off={off || !custom}
                                waiting={waiting}
                                working={busy === slot}
                                workingWords='Removing…'
                                title={custom ? 'Delete the uploaded picture: the channel then has none' : 'Nothing has been uploaded for this channel'}
                                confirms={true}
                                armed={confirm.armed === `revert:${slot}`}
                                onPress={(event) => {
                                    const hint = `Click again to delete the uploaded ${wide ? 'card picture' : 'picture'} for ${label}.`;
                                    if (confirm.press(`revert:${slot}`, hint, event)) {
                                        revert(channel.key, channel.handle, kind);
                                    }
                                }}
                            >{'Remove'}</AdminPill>
                        </div>
                    )}

                    <input
                        ref={(node) => {
                            pickers.current[slot] = node;
                        }}
                        type='file'
                        accept='image/png,image/jpeg,image/gif,image/webp,image/svg+xml'
                        className='vntv-admin-file'
                        tabIndex={-1}
                        onChange={(event) => {
                            choose(channel.key, kind, event.target.files?.[0]);

                            // Cleared so choosing the same file twice still fires.
                            event.target.value = '';
                        }}
                    />
                </div>
            </li>
        );
    };

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead instant={true}>
                {channels.length === 0 ?
                    'No 24/7 stream keys are saved. Add one under 24/7 stream keys and save, and it appears here.' :
                    `${channels.length} channel${channels.length === 1 ? '' : 's'}, two pictures each.`}
            </AdminHead>

            {channels.map((channel) => (
                <section key={channel.key}>
                    <h4 className='vntv-admin-sub'>
                        {channel.name || channel.key}
                        {channel.name ? <span className='vntv-key vntv-admin-subkey'>{` ${channel.key}`}</span> : null}
                    </h4>
                    <ul className='vntv-admin-list'>
                        {slot(channel, 'picture')}
                        {slot(channel, 'thumb')}
                    </ul>
                </section>
            ))}

            <AdminStatus
                ref={statusRef}
                note={confirm.hint || note}
                error={error || (loadError === '' ? '' : `The 24/7 channels could not be loaded: ${loadError}`)}
            />
        </AdminCard>
    );
};
