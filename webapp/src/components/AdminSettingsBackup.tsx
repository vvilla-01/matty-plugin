import React from 'react';

import {exportSettings, importSettings} from '../client';
import type {SettingsImportResult} from '../client';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardFocus} from './adminParts';
import type {AdminCardProps} from './adminParts';

// Every setting on this page, out to a file and back in again, from System
// Console > Plugins > Matty.
//
// Two things this deliberately does NOT pretend to be:
//
//   * A full backup. Recorded airtime has its own Export under Airtime,
//     because it is the irreplaceable half and does not belong in the same file
//     as a banner colour. The setting's help text, under this card, says what
//     the file leaves out rather than leaving somebody to find out during a
//     restore.
//   * A setting. It edits no config value, takes no value prop and never calls
//     onChange, so nothing here can leave the page's Save button dirty.
//
// An import writes the whole configuration and the page is still showing the old
// one, so pressing Save afterwards would put it all straight back. So the page
// is reloaded — by itself when the result is one line, read in a moment; by
// the admin, with a pill, when the server had more to say (settings kept,
// settings it did not know, a warning), which a reload would wipe away before
// anybody could read it.
const RELOAD_AFTER_MS = 1800;

const needsReading = (result: SettingsImportResult): boolean =>
    Boolean(result.warning) || (result.kept || []).length > 0 || (result.unknown || []).length > 0;

export const AdminSettingsBackup: React.FC<AdminCardProps> = ({id, label, disabled}) => {
    const [withSecrets, setWithSecrets] = React.useState(false);
    const [busy, setBusy] = React.useState<'' | 'save' | 'restore'>('');
    const [result, setResult] = React.useState<SettingsImportResult | null>(null);
    const [error, setError] = React.useState('');
    const picker = React.useRef<HTMLInputElement>(null);
    const {statusRef, focus} = useCardFocus();

    const save = React.useCallback(() => {
        setBusy('save');
        setError('');
        setResult(null);

        exportSettings(withSecrets).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(''));
    }, [withSecrets]);

    const restore = React.useCallback((file?: File) => {
        if (!file) {
            return;
        }

        setBusy('restore');
        setError('');
        setResult(null);

        file.text().
            then(importSettings).
            then((next) => {
                setResult(next);
                if (next.ok && !needsReading(next)) {
                    window.setTimeout(() => window.location.reload(), RELOAD_AFTER_MS);
                }
            }).
            catch((reason: unknown) => {
                setError(reason instanceof Error ? reason.message : String(reason));
            }).
            finally(() => setBusy(''));
    }, []);

    const restored = result?.ok ? result : null;
    const waitsForReload = restored !== null && needsReading(restored);
    let note = '';
    if (restored) {
        const changed = `${restored.changed} setting${restored.changed === 1 ? '' : 's'} changed${restored.from ? `, from a backup written by v${restored.from}` : ''}.`;
        note = waitsForReload ?
            `${changed} Read what is below, then reload the page before you save anything here: Save would put the old values back.` :
            `${changed} Reloading the page so it shows them…`;
    }

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead instant={true}>
                {'Every setting on this page, the birthday list and the theme included, as one JSON file.'}
            </AdminHead>

            <label className='vntv-admin-check'>
                <input
                    type='checkbox'
                    checked={withSecrets}
                    disabled={disabled || busy !== ''}
                    onChange={(event) => setWithSecrets(event.target.checked)}
                />
                {'Include the passwords and keys (Basic Auth, Mumble, the TMDB token, the Twitch and Hytale client secrets and the Steam Web API key)'}
            </label>

            {/* Not "safe to keep anywhere" without them, as this used to say:
                the 24/7 channels, the stream names and the stream notes are
                all written under stream keys, and a stream key is directly
                usable against Broadcast Box by anything that can reach it
                (LOCKDOWN §4). */}
            <p className='vntv-admin-line vntv-admin-quiet vntv-admin-checknote'>
                {withSecrets ?
                    'They will be in the file in plain text. Keep it somewhere you would keep a password.' :
                    'Without them a restore leaves the installed passwords and keys alone. The file still names stream keys (the 24/7 channels, and the stream names and notes, are listed by key), so keep it as privately as this page.'}
            </p>

            <div className='vntv-admin-actions'>
                <AdminPill
                    tone='primary'
                    off={disabled}
                    waiting={busy !== ''}
                    working={busy === 'save'}
                    workingWords='Working…'
                    title='Download every setting on this page as JSON'
                    onPress={save}
                >{'Back up'}</AdminPill>
                <AdminPill
                    off={disabled}
                    waiting={busy !== ''}
                    working={busy === 'restore'}
                    workingWords='Restoring…'
                    title='Choose a backup file to restore'
                    onPress={() => picker.current?.click()}
                >{'Restore…'}</AdminPill>
            </div>

            {/* Hidden, because a bare file input beside two pills looks like a
                different kind of control from the one it is. */}
            <input
                ref={picker}
                type='file'
                accept='application/json,.json'
                className='vntv-admin-file'
                tabIndex={-1}
                onChange={(event) => {
                    restore(event.target.files?.[0]);

                    // Cleared so choosing the same file twice still fires.
                    event.target.value = '';
                }}
            />

            <AdminStatus
                ref={statusRef}
                note={note}
                error={error || restored?.warning || ''}
            />

            {restored && (
                <>
                    {(restored.kept || []).length > 0 && (
                        <p className='vntv-admin-line vntv-admin-quiet'>
                            {`That file carried no passwords, so these were left as they are: ${(restored.kept || []).join(', ')}.`}
                        </p>
                    )}

                    {(restored.unknown || []).length > 0 && (
                        <p className='vntv-admin-line vntv-admin-quiet'>
                            {`Ignored, because this version has no such setting: ${(restored.unknown || []).join(', ')}.`}
                        </p>
                    )}

                    {waitsForReload ? (
                        <div className='vntv-admin-actions'>
                            <AdminPill
                                tone='primary'
                                onPress={() => window.location.reload()}
                            >{'Reload the page'}</AdminPill>
                        </div>
                    ) : null}
                </>
            )}
        </AdminCard>
    );
};
