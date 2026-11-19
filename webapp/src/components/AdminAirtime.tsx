import React from 'react';

import {exportAirtime, fetchAirtimeStatus, importAirtime, restoreAirtime} from '../client';
import {formatDuration} from '../format';
import type {AirtimeStatus} from '../types';

import {ledgerLines} from './AdminAlerts';
import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardFocus} from './adminParts';

// Rendered inside System Console > Plugins > Matty, as the component
// for the AirtimeTools setting in plugin.json. Airtime is admin data: backing
// it up and putting it back are things an admin does deliberately, on the page
// where the rest of the plugin is configured, not from the panel everyone uses
// to watch television.
//
// Mattermost hands a custom setting a pile of props for editing a config value
// (value, onChange, registerSaveAction…). This edits no config value, so it
// takes only disabled and never calls onChange: nothing here can leave the Save
// button in a dirty state.

// Rendered as a plain UTC timestamp, to match the rest of the plugin: which day
// a backup was taken should not depend on where the reader is sitting.
function whenUTC(millis: number): string {
    if (!millis) {
        return 'never';
    }

    const at = new Date(millis);
    const pad = (value: number) => String(value).padStart(2, '0');

    return `${at.getUTCFullYear()}-${pad(at.getUTCMonth() + 1)}-${pad(at.getUTCDate())} ` +
        `${pad(at.getUTCHours())}:${pad(at.getUTCMinutes())} UTC`;
}

type Props = {
    id?: string;
    label?: string;

    // Mattermost's: the plugin is off, or this admin may read the settings
    // but not change them. Restoring and importing change the history, so
    // they wait; exporting and looking only read it.
    disabled?: boolean;
};

type Busy = '' | 'export' | 'restore' | 'import';

const BUSY_WORDS: Record<Exclude<Busy, ''>, string> = {
    export: 'Exporting…',
    restore: 'Restoring…',
    import: 'Importing…',
};

const streams = (count: number): string => `${count} stream${count === 1 ? '' : 's'}`;

export const AdminAirtime: React.FC<Props> = ({id, label, disabled}) => {
    const [status, setStatus] = React.useState<AirtimeStatus | null>(null);
    const [error, setError] = React.useState('');
    const [busy, setBusy] = React.useState<Busy>('');
    const [note, setNote] = React.useState('');
    const [failed, setFailed] = React.useState('');
    const picker = React.useRef<HTMLInputElement>(null);
    const {statusRef, focus} = useCardFocus();

    const load = React.useCallback(() => {
        fetchAirtimeStatus().then((next) => {
            setStatus(next);
            setError('');
        }).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        });
    }, []);

    React.useEffect(load, [load]);

    const restore = React.useCallback(() => {
        setBusy('restore');
        setNote('');
        setFailed('');

        restoreAirtime().then((result) => {
            setNote(`${result.message} ${result.changed} streamer${result.changed === 1 ? '' : 's'} updated.`);
            load();
        }).catch((reason: unknown) => {
            setFailed(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(''));
    }, [load]);

    const save = React.useCallback(() => {
        setBusy('export');
        setNote('');
        setFailed('');

        exportAirtime().catch((reason: unknown) => {
            setFailed(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(''));
    }, []);

    // Taking an export back in. The one path by which recorded hours move
    // between servers — or between plugin IDs, which Mattermost treats as two
    // different plugins with two different stores, so a rename of the id starts
    // the history at zero unless it comes across this way.
    const bringIn = React.useCallback((file?: File) => {
        if (!file) {
            return;
        }

        setBusy('import');
        setNote('');
        setFailed('');

        file.text().
            then(importAirtime).
            then((result) => {
                setNote(`${result.message} ${result.changed} streamer${result.changed === 1 ? '' : 's'} updated.`);
                load();
            }).
            catch((reason: unknown) => {
                setFailed(reason instanceof Error ? reason.message : String(reason));
            }).
            finally(() => setBusy(''));
    }, [load]);

    if (error !== '' || !status) {
        return (
            <AdminCard
                id={id}
                label={label}
                focus={focus}
            >
                <AdminHead instant={true}>{error === '' ? 'Loading…' : 'The recorded airtime could not be read just now.'}</AdminHead>
                {error === '' ? null : (
                    <div className='vntv-admin-actions'>
                        <AdminPill onPress={load}>{'Refresh'}</AdminPill>
                    </div>
                )}
                <AdminStatus
                    ref={statusRef}
                    note=''
                    error={error}
                />
            </AdminCard>
        );
    }

    const missing = status.backupAheadSeconds;
    const working = busy !== '';

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead instant={true}>
                {status.readable ? (
                    <>
                        <b>{formatDuration(status.recordedSeconds)}</b>
                        {` recorded across ${streams(status.streamers)}`}
                        {status.months > 0 ? ` and ${status.months} month${status.months === 1 ? '' : 's'}` : ''}
                    </>
                ) : 'The stored airtime could not be read.'}
            </AdminHead>

            <p className='vntv-admin-line vntv-admin-quiet'>
                {status.hasBackup ? (
                    `Backed up ${whenUTC(status.backupSavedAt || 0)} by v${status.backupVersion || '?'}: ` +
                    `${formatDuration(status.backupSeconds || 0)} across ${streams(status.backupStreamers || 0)}.`
                ) : 'No backup yet. One is taken every time the plugin starts.'}
            </p>

            {!status.recording && !status.halted && (
                <p className='vntv-admin-line vntv-admin-quiet'>
                    {'Recording is switched off: nothing new is counted, and what is counted is kept.'}
                </p>
            )}

            {status.twitchSeparate && (
                <p className='vntv-admin-line vntv-admin-quiet'>
                    {'Twitch hours are kept separately; see Twitch connections.'}
                </p>
            )}

            {missing > 0 && (
                <div className='vntv-admin-callout' data-tone='warning'>
                    <p className='vntv-admin-line'>
                        {'Recorded airtime is '}
                        <b>{formatDuration(missing)}</b>
                        {' short of that backup. Restore backup puts back what is missing and takes nothing away.'}
                    </p>
                </div>
            )}

            {status.halted && (
                <div className='vntv-admin-callout' data-tone='error'>
                    <p className='vntv-admin-line'>
                        {'Recording has stopped so the stored history is not overwritten: '}
                        {status.haltedReason}
                    </p>
                </div>
            )}

            {/* The fan and 24/7 channel ledgers (design/3.2.0.md 4.7), in
                AdminAlerts' words. */}
            {ledgerLines({fansResetAt: status.fansResetAt, channelsResetAt: status.channelsResetAt}).map((line) => (
                <div
                    key={line}
                    className='vntv-admin-callout'
                    data-tone='warning'
                >
                    <p className='vntv-admin-line'>{line}</p>
                </div>
            ))}

            {(status.quarantined || []).length > 0 && (
                <div className='vntv-admin-callout' data-tone='error'>
                    <p className='vntv-admin-line'>
                        {`Kept aside because it could not be read: ${(status.quarantined || []).join(', ')}. `}
                        {'The original bytes are in this plugin’s KV store under a quarantine_ key.'}
                    </p>
                </div>
            )}

            <div className='vntv-admin-actions'>
                <AdminPill
                    tone='primary'
                    waiting={working}
                    working={busy === 'export'}
                    workingWords={BUSY_WORDS.export}
                    title='Download every recorded hour as JSON'
                    onPress={save}
                >{'Export'}</AdminPill>
                <AdminPill
                    off={disabled || !status.hasBackup}
                    waiting={working}
                    working={busy === 'restore'}
                    workingWords={BUSY_WORDS.restore}
                    title={status.hasBackup ? 'Merge the backup back in, keeping the larger figure per streamer' : 'There is no backup to restore yet'}
                    onPress={restore}
                >{'Restore backup'}</AdminPill>
                <AdminPill
                    off={disabled}
                    waiting={working}
                    working={busy === 'import'}
                    workingWords={BUSY_WORDS.import}
                    title='Take an exported airtime file back in, keeping the larger figure per streamer'
                    onPress={() => picker.current?.click()}
                >{'Import…'}</AdminPill>
                <AdminPill
                    waiting={working}
                    onPress={load}
                >{'Refresh'}</AdminPill>
            </div>

            <input
                ref={picker}
                type='file'
                accept='application/json,.json'
                className='vntv-admin-file'
                tabIndex={-1}
                onChange={(event) => {
                    bringIn(event.target.files?.[0]);

                    // Cleared so choosing the same file twice still fires.
                    event.target.value = '';
                }}
            />

            <AdminStatus
                ref={statusRef}
                note={note}
                error={failed}
            />
        </AdminCard>
    );
};
