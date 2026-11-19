import React from 'react';

import {fetchAdminAppCheck, fetchRelease} from '../client';
import type {AdminAppCheck as AppCheckView, AdminAppCheckVerdict} from '../client';
import {fingerprint, sha256Hex} from '../sha256';
import {postedAt, postedWhen, viewerZone} from '../time';
import type {ReleaseAnswer} from '../types';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardFocus} from './adminParts';
import type {AdminCardProps} from './adminParts';

// System Console > Plugins > Matty > App check, the AppCheckList setting:
// first one quiet line about this plugin's own files (GET /api/v1/release),
// then who has proved a genuine Matty and who has not (GET
// /api/v1/admin/appcheck), newest first, with the counts over them.
//
// The plugin's line is here rather than under Troubleshooting because this is
// where an admin is already looking at signatures: the release is signed with
// the same two keys as the app, and the app checks it against the same
// certificate this section accepts. It says what the server found, in words,
// and the manifest's SHA-256, worked out here from the manifest the server
// sent, for comparing with the release's page.
//
// Read-only: nothing here is saved, so it never calls onChange. A verdict
// shows what the server sends of it and no more: who, when, verified or not
// and why in the server's own words, the app's version code as Android
// attested it, and whether only the development build would pass it. The
// phone's security level and boot state, the session and what the app says
// of itself are kept by the server and not shown.

// Past this many sign-ins the list shows the newest and a button for the
// rest.
const SHOWN_FIRST = 20;

const plural = (n: number, one: string, many: string): string => `${n} ${n === 1 ? one : many}`;

type Row = AdminAppCheckVerdict & {userId: string; username?: string};

// Every sign-in's verdict, newest first.
function rowsOf(view: AppCheckView): Row[] {
    return view.people.flatMap((person) => (person.sessions || []).map((session) => ({...session, userId: person.userId, username: person.username}))).
        sort((a, b) => b.at - a.at);
}

type State = 'verified' | 'unverified' | 'stale';

const stateOf = (row: Row): State => {
    if (row.stale) {
        return 'stale';
    }

    return row.verified ? 'verified' : 'unverified';
};

const STATE_WORDS: Record<State, string> = {
    verified: 'Verified',
    unverified: 'Not verified',
    stale: 'No longer accepted',
};

// What the setting does now, in a line.
function modeLine(check: string): string {
    switch (check) {
    case 'off':
        return 'The check is off: no Matty is asked to prove itself, and nobody is refused.';
    case 'require':
        return 'Each Matty proves itself when it opens, and one that has not passed is refused everywhere but its lock screen.';
    default:
        return 'Each Matty proves itself when it opens, and what it found is shown to that person and here. Nobody is refused.';
    }
}

// What this plugin's files are, in words.
function releaseWords(release: ReleaseAnswer): {text: string; error: boolean} {
    const version = release.version ? ` ${release.version}` : '';
    switch (release.state) {
    case 'signed':
        return {text: `This plugin is a signed release of Matty Plugin${version}.`, error: false};
    case 'unsigned':
        return {text: `This plugin is not a signed release: a development build, or somebody’s own build of Matty Plugin${version}.`, error: false};
    case 'altered': {
        const reason = (release.reason || '').trim();

        return {text: `This plugin’s files are altered. They are not as the signed release of Matty Plugin${version} has them${reason ? `: ${reason.replace(/[.]$/, '')}` : ''}.`, error: true};
    }
    default:
        return {text: `This plugin’s release could not be told: the server says “${release.state}”.`, error: true};
    }
}

export const AdminAppCheck: React.FC<AdminCardProps> = ({id, label}) => {
    const [release, setRelease] = React.useState<ReleaseAnswer | null>(null);
    const [releaseError, setReleaseError] = React.useState('');
    const [view, setView] = React.useState<AppCheckView | null>(null);
    const [error, setError] = React.useState('');
    const [loading, setLoading] = React.useState(true);
    const [all, setAll] = React.useState(false);
    const {statusRef, focus} = useCardFocus();

    const alive = React.useRef(true);
    React.useEffect(() => {
        alive.current = true;

        return () => {
            alive.current = false;
        };
    }, []);

    const load = React.useCallback(() => {
        setLoading(true);
        setError('');
        setReleaseError('');

        const releaseRead = fetchRelease().then((answer) => {
            if (alive.current) {
                setRelease(answer);
            }
        }).catch((reason: unknown) => {
            if (alive.current) {
                setRelease(null);
                setReleaseError(reason instanceof Error ? reason.message : String(reason));
            }
        });

        const listRead = fetchAdminAppCheck().then((answer) => {
            if (alive.current) {
                setView(answer);
            }
        }).catch((reason: unknown) => {
            if (alive.current) {
                setView(null);
                setError(reason instanceof Error ? reason.message : String(reason));
            }
        });

        Promise.all([releaseRead, listRead]).finally(() => {
            if (alive.current) {
                setLoading(false);
            }
        });
    }, []);

    React.useEffect(load, [load]);

    // Worked out once for each manifest the server sends.
    const manifestSum = React.useMemo(() => (release?.manifest ? fingerprint(sha256Hex(release.manifest)) : ''), [release?.manifest]);

    const zone = viewerZone();
    const rows = view ? rowsOf(view) : [];
    const shown = all ? rows : rows.slice(0, SHOWN_FIRST);
    const counts = {verified: 0, unverified: 0, stale: 0};
    rows.forEach((row) => {
        counts[stateOf(row)] += 1;
    });

    let head: React.ReactNode;
    if (view === null) {
        head = loading ? 'Loading…' : 'The app check’s results could not be loaded.';
    } else if (rows.length === 0) {
        head = 'No Matty has a verdict here: none has been checked yet, or every verdict has expired.';
    } else {
        const parts = [
            counts.verified > 0 ? `${counts.verified} verified` : '',
            counts.unverified > 0 ? `${counts.unverified} not verified` : '',
            counts.stale > 0 ? `${counts.stale} no longer accepted` : '',
        ].filter(Boolean);
        head = (
            <>
                <b>{plural(rows.length, 'sign-in', 'sign-ins')}</b>
                {` of ${plural(view.people.length, 'person', 'people')}: ${parts.join(', ')}`}
            </>
        );
    }

    const releaseLine = release ? releaseWords(release) : null;

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <h4 className='vntv-admin-sub vntv-appcheck-first'>{'This plugin'}</h4>
            {releaseLine ? (
                <p className={releaseLine.error ? 'vntv-admin-line vntv-notice-error' : 'vntv-admin-line vntv-admin-quiet'}>{releaseLine.text}</p>
            ) : (
                <p className={releaseError ? 'vntv-admin-line vntv-notice-error' : 'vntv-admin-line vntv-admin-quiet'}>
                    {releaseError ? `Whether this plugin is a signed release could not be read: ${releaseError}` : 'Loading…'}
                </p>
            )}
            {manifestSum !== '' && (
                <p className='vntv-admin-line vntv-admin-quiet'>
                    {'Its manifest’s SHA-256, to compare with the one on the release’s page: '}
                    <code className='vntv-admin-code vntv-appcheck-sum'>
                        {/* A line may break after any colon, never inside a pair. */}
                        {manifestSum.split(':').map((pair, at) => (
                            <React.Fragment key={at}>
                                {at > 0 ? ':' : ''}
                                {at > 0 ? <wbr/> : null}
                                {pair}
                            </React.Fragment>
                        ))}
                    </code>
                </p>
            )}
            {releaseLine && release?.state !== 'unsigned' ? (
                <p className='vntv-admin-line vntv-admin-quiet'>
                    {'This is the plugin’s own check of its files when it started: evidence, not a lock, since a server program that was altered can skip it. It shows which release this server presents, not what code it runs.'}
                </p>
            ) : null}

            <h4 className='vntv-admin-sub'>{'Matty apps'}</h4>
            <AdminHead>{head}</AdminHead>
            {view ? <p className='vntv-admin-line'>{modeLine(view.check)}</p> : null}
            {view?.problem ? <p className='vntv-admin-line vntv-notice-error'>{view.problem}</p> : null}

            {/* What a verdict is worth, said where the verdicts are. */}
            <p className='vntv-admin-line vntv-admin-quiet'>
                {'Verified means Android’s secure hardware vouched for that sign-in: the official Matty, signed with Matty’s certificate (or one added above), on a phone whose bootloader is locked and whose system is verified, or signed by a key this server accepts (GrapheneOS’s, or one added above). '}
                {'It cannot tell the web panel, or a program that does not say it is Matty, from anything else, and it holds only while the phone’s secure hardware is itself intact. '}
                {'A verdict lasts 30 days, or until that sign-in ends.'}
            </p>

            {shown.length > 0 && (
                <ul className='vntv-admin-list'>
                    {shown.map((row) => {
                        const state = stateOf(row);
                        const reason = state === 'verified' ? '' : (row.reason || '').trim();

                        return (
                            <li
                                key={`${row.userId}:${row.at}:${row.expires}`}
                                className='vntv-admin-row'
                                data-align='top'
                            >
                                <div className='vntv-admin-row-main'>
                                    <p className='vntv-admin-row-title'>
                                        <span className='vntv-admin-row-name'>{row.username ? `@${row.username}` : `User ${row.userId}`}</span>
                                        <span
                                            className='vntv-admin-flag'
                                            data-tone={state === 'verified' ? 'accent' : undefined}
                                            data-state={state}
                                        >{STATE_WORDS[state]}</span>
                                        {state === 'verified' && row.development ? (
                                            <span
                                                className='vntv-admin-flag'
                                                title='Only the development build of this plugin would pass it: an emulator, or a debug build of the app'
                                            >{'Development'}</span>
                                        ) : null}
                                    </p>
                                    <p className='vntv-admin-row-meta'>
                                        <time
                                            dateTime={new Date(row.at).toISOString()}
                                            title={postedAt(row.at, zone)}
                                        >{postedWhen(row.at, zone)}</time>
                                        {row.versionCode ? ` · app version code ${row.versionCode}` : ''}
                                    </p>
                                    {reason !== '' && <p className='vntv-admin-row-meta vntv-appcheck-reason'>{reason}</p>}
                                </div>
                            </li>
                        );
                    })}
                </ul>
            )}

            <div className='vntv-admin-actions'>
                {rows.length > shown.length ? (
                    <AdminPill onPress={() => setAll(true)}>{`Show all ${rows.length}`}</AdminPill>
                ) : null}
                <AdminPill
                    waiting={loading}
                    onPress={load}
                >{'Refresh'}</AdminPill>
            </div>

            <AdminStatus
                ref={statusRef}
                note=''
                error={error}
            />
        </AdminCard>
    );
};
