import React from 'react';

import {fetchAdminPush} from '../client';
import type {AdminPush as PushView} from '../client';
import {pushDroppedLine, pushFullLine, pushHead, pushHostFlag, pushHostParts, pushSwitchLines, pushTroubleLines} from '../pushAdmin';
import {postedAt, postedWhen, viewerZone} from '../time';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardFocus} from './adminParts';
import type {AdminCardProps} from './adminParts';

// System Console > Plugins > Matty > Push notifications > Status, the
// PushStatus setting (GET /api/v1/admin/push): whether pushes can go out —
// the plugin's switch and Mattermost's own, with where that is switched on —
// then how many phones of how many people are registered, and one row per
// push server host with its phones working, waiting for their first push,
// failing (and since when) and refused. Never a row per person: which push
// server somebody uses is theirs to know, and the server sends only counts.
//
// Read-only: nothing here is saved, so it never calls onChange. The words
// are pushAdmin.ts's, which build/check-away.mjs holds.
export const AdminPush: React.FC<AdminCardProps> = ({id, label}) => {
    const [view, setView] = React.useState<PushView | null>(null);
    const [error, setError] = React.useState('');
    const [loading, setLoading] = React.useState(true);
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

        fetchAdminPush().then((answer) => {
            if (alive.current) {
                setView(answer);
            }
        }).catch((reason: unknown) => {
            if (alive.current) {
                setView(null);
                setError(reason instanceof Error ? reason.message : String(reason));
            }
        }).finally(() => {
            if (alive.current) {
                setLoading(false);
            }
        });
    }, []);

    React.useEffect(load, [load]);

    const zone = viewerZone();
    let head: React.ReactNode;
    if (view === null) {
        head = loading ? 'Loading…' : 'Push notifications’ state could not be loaded.';
    } else {
        const {bold, rest} = pushHead(view);
        head = (
            <>
                {bold ? <b>{bold}</b> : null}
                {rest}
            </>
        );
    }

    const hosts = view ? view.hosts : [];
    const full = view ? pushFullLine(view) : '';
    const dropped = view ? pushDroppedLine(view) : '';

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead>{head}</AdminHead>
            {view ? pushSwitchLines(view).map((line) => (
                <p
                    key={line.text}
                    className={line.tone === 'error' ? 'vntv-admin-line vntv-notice-error' : `vntv-admin-line${line.tone === 'quiet' ? ' vntv-admin-quiet' : ''}`}
                >{line.text}</p>
            )) : null}
            {view?.problem ? <p className='vntv-admin-line vntv-notice-error'>{view.problem}</p> : null}
            {full ? <p className='vntv-admin-line'>{full}</p> : null}

            {hosts.length > 0 && (
                <>
                    <h4 className='vntv-admin-sub'>{'Push servers'}</h4>
                    <ul className='vntv-admin-list'>
                        {hosts.map((row) => {
                            const flag = pushHostFlag(row);
                            const parts = pushHostParts(row, (at) => postedWhen(at, zone));

                            return (
                                <li
                                    key={row.host}
                                    className='vntv-admin-row'
                                    data-align='top'
                                >
                                    <div className='vntv-admin-row-main'>
                                        <p className='vntv-admin-row-title'>
                                            <span className='vntv-admin-row-name'>{row.host}</span>
                                            {flag ? (
                                                <span
                                                    className='vntv-admin-flag'
                                                    data-state={flag.state}
                                                >{flag.word}</span>
                                            ) : null}
                                        </p>
                                        <p
                                            className='vntv-admin-row-meta'
                                            title={row.failingSince ? `Failing since ${postedAt(row.failingSince, zone)}` : undefined}
                                        >{parts.join(' · ')}</p>
                                    </div>
                                </li>
                            );
                        })}
                    </ul>
                    {view ? pushTroubleLines(view).map((line) => (
                        <p
                            key={line}
                            className='vntv-admin-line vntv-admin-quiet vntv-push-why'
                        >{line}</p>
                    )) : null}
                </>
            )}

            {dropped ? <p className='vntv-admin-line vntv-admin-quiet vntv-push-why'>{dropped}</p> : null}

            <div className='vntv-admin-actions'>
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
