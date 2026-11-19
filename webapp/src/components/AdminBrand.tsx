import React from 'react';

import {brandURL, fetchBrand, resetBrand, uploadBrand} from '../client';

import {PRODUCT_NAME} from '../name';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm, useCardFocus} from './adminParts';
import type {AdminCardProps} from './adminParts';

// The artwork, changed from System Console.
//
// Three pieces, because they are three different jobs: the LOGO is the emblem
// faded into the corner of the panel, seen at roughly four hundred pixels; the
// ICON is the button in the App Bar, seen at twenty-four; and the GHOST is what
// the No Signal screen catches through its static now and then. Only the icon
// is built in (Matty's own), so it can go back to that; the emblem and the
// ghost exist only once an admin uploads them, and Remove leaves none.
//
// Saves IMMEDIATELY, not with the page's Save button, and says so in its head;
// what it accepts and who sees a change when are in the setting's help text,
// which Mattermost puts under it. It edits no
// configuration value and never calls onChange, so nothing here can leave the
// Save button in a dirty state — the image goes to the plugin's own store, for
// the same reason the bot's picture goes to Mattermost's: a few hundred
// kilobytes of base64 in a config value would be written to disk, held in
// memory and shipped to every node on every configuration change.

type Piece = {
    which: string;
    label: string;
    hint: string;

    // How the preview is shown. The ghost is seen through the No Signal
    // screen's dark static, so its preview is dark; the emblem sits on the
    // panel's own ground, the glyph in Mattermost's App Bar at its own size.
    preview: 'mark' | 'ghost' | 'glyph';

    // Whether there is one when nothing is uploaded: the icon only.
    builtIn: boolean;
};

const PIECES: Piece[] = [
    {
        which: 'logo',
        label: 'Panel emblem',
        hint: 'Faded into the panel’s bottom corner at about 400px, and behind the lock’s padlock. Square artwork works best. Without one there is neither.',
        preview: 'mark',
        builtIn: false,
    },
    {
        which: 'icon',
        label: 'App Bar icon',
        hint: `Opens the panel, drawn at 24px, where detail disappears. Keep it simple. ${PRODUCT_NAME}’s own until another is uploaded.`,
        preview: 'glyph',
        builtIn: true,
    },
    {
        which: 'ghost',
        label: 'No Signal ghost',
        hint: 'Flickers through the No Signal static now and then. Use a transparent background: an opaque one shows as a pale square. Without one, No Signal is static only.',
        preview: 'ghost',
        builtIn: false,
    },
];

const nameOf = (which: string): string => PIECES.find((piece) => piece.which === which)?.label || which;

export const AdminBrand: React.FC<AdminCardProps> = ({id, label, disabled}) => {
    const [custom, setCustom] = React.useState<Record<string, boolean>>({});
    const [stamp, setStamp] = React.useState(() => Date.now());
    const [busy, setBusy] = React.useState('');
    const [note, setNote] = React.useState('');
    const [error, setError] = React.useState('');
    const pickers = React.useRef<Record<string, HTMLInputElement | null>>({});
    const {statusRef, focus} = useCardFocus();

    // Removing an upload, or going back to the built-in icon, deletes the
    // uploaded file, so it takes a second press.
    const confirm = useCardConfirm(() => {
        setNote('');
        setError('');
    });

    const load = React.useCallback(() => {
        fetchBrand().then(setCustom).catch(() => setCustom({}));
    }, []);

    React.useEffect(load, [load]);

    // The images are served with "no-cache" rather than "never cache", so the
    // browser revalidates — but it will happily reuse what it has within the
    // same page. A changed stamp is what makes the preview show the new file
    // the instant it is saved.
    const refresh = React.useCallback(() => {
        setStamp(Date.now());
        load();
    }, [load]);

    const choose = React.useCallback((which: string, file?: File) => {
        if (!file) {
            return;
        }

        setBusy(`upload:${which}`);
        setNote('');
        setError('');

        uploadBrand(which, file).then(() => {
            setNote(`${nameOf(which)} saved.`);
            refresh();
        }).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(''));
    }, [refresh]);

    const revert = React.useCallback((which: string) => {
        setBusy(`revert:${which}`);
        setNote('');
        setError('');

        resetBrand(which).then(() => {
            setNote(which === 'icon' ? `The App Bar icon is ${PRODUCT_NAME}’s own again.` : `${nameOf(which)} removed: there is none now.`);
            refresh();
        }).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(''));
    }, [refresh]);

    const replaced = PIECES.filter((piece) => custom[piece.which]).length;

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead instant={true}>
                {replaced === 0 ? `Nothing uploaded: ${PRODUCT_NAME}’s own icon, and no emblem or ghost.` : `${replaced} of 3 uploaded.`}
            </AdminHead>

            <ul className='vntv-admin-list'>
                {PIECES.map((piece) => {
                    const uploaded = Boolean(custom[piece.which]);
                    const none = !uploaded && !piece.builtIn;

                    return (
                        <li
                            key={piece.which}
                            className='vntv-admin-row'
                            data-align='top'
                        >
                            <span
                                className='vntv-admin-thumb'
                                data-dark={piece.preview === 'ghost' && !none ? 'true' : undefined}
                                data-pad={piece.preview === 'glyph' ? 'large' : 'small'}
                                data-fit='contain'
                            >
                                {/* Nothing to ask for when there is none: the
                                    server answers 404. */}
                                {none ? (
                                    <span className='vntv-admin-thumb-none vntv-brand-none'>{'None'}</span>
                                ) : (
                                    <img
                                        className='vntv-admin-thumb-image'
                                        src={brandURL(piece.which, stamp)}
                                        alt={`The current ${piece.label.toLowerCase()}`}
                                    />
                                )}
                            </span>

                            <div className='vntv-admin-row-main'>
                                <p className='vntv-admin-row-title'>
                                    <span className='vntv-admin-row-name'>{piece.label}</span>
                                    {uploaded ? (
                                        <span
                                            className='vntv-admin-flag'
                                            data-tone='accent'
                                        >{'Uploaded'}</span>
                                    ) : null}
                                </p>
                                <p className='vntv-admin-row-meta'>{piece.hint}</p>

                                <div
                                    className='vntv-admin-actions'
                                    role='group'
                                    aria-label={piece.label}
                                >
                                    <AdminPill
                                        off={disabled}
                                        waiting={busy !== ''}
                                        working={busy === `upload:${piece.which}`}
                                        workingWords='Saving…'
                                        onPress={() => pickers.current[piece.which]?.click()}
                                    >{'Choose a file'}</AdminPill>

                                    <AdminPill
                                        off={disabled || !uploaded}
                                        waiting={busy !== ''}
                                        working={busy === `revert:${piece.which}`}
                                        workingWords={piece.builtIn ? 'Restoring…' : 'Removing…'}
                                        title={(() => {
                                            if (piece.builtIn) {
                                                return uploaded ? `Go back to ${PRODUCT_NAME}’s own icon` : `This is already ${PRODUCT_NAME}’s own icon`;
                                            }

                                            return uploaded ? `Delete the uploaded ${piece.label.toLowerCase()}: there is then none` : 'Nothing has been uploaded';
                                        })()}
                                        confirms={true}
                                        armed={confirm.armed === `revert:${piece.which}`}
                                        onPress={(event) => {
                                            const hint = piece.builtIn ?
                                                `Click again to delete the uploaded ${piece.label.toLowerCase()} and go back to ${PRODUCT_NAME}’s own.` :
                                                `Click again to delete the uploaded ${piece.label.toLowerCase()}: there is then none.`;
                                            if (confirm.press(`revert:${piece.which}`, hint, event)) {
                                                revert(piece.which);
                                            }
                                        }}
                                    >{piece.builtIn ? `Use ${PRODUCT_NAME}’s icon` : 'Remove'}</AdminPill>
                                </div>

                                <input
                                    ref={(node) => {
                                        pickers.current[piece.which] = node;
                                    }}
                                    type='file'
                                    accept='image/svg+xml,image/png,image/jpeg,image/gif,image/webp'
                                    className='vntv-admin-file'
                                    tabIndex={-1}
                                    onChange={(event) => {
                                        choose(piece.which, event.target.files?.[0]);

                                        // Cleared so choosing the same file twice still fires.
                                        event.target.value = '';
                                    }}
                                />
                            </div>
                        </li>
                    );
                })}
            </ul>

            <AdminStatus
                ref={statusRef}
                note={confirm.hint || note}
                error={error}
            />
        </AdminCard>
    );
};
