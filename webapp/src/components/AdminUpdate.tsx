import React from 'react';

import {checkAdminUpdate, fetchAdminUpdate, fetchUpdateBundle, uploadPluginBundle} from '../client';
import type {AdminUpdate as UpdateView} from '../client';
import {postedWhen, viewerZone} from '../time';
import {
    INSTALL_IDLE,
    installButtonText,
    installButtonVersion,
    installButtons,
    installNext,
    installOffered,
    installQuestion,
    installStepText,
    updateHead,
    updateLines,
    updatePageLinkable,
} from '../updateAdmin';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardFocus} from './adminParts';
import type {AdminCardProps} from './adminParts';

// System Console > Plugins > Matty > Updates > Status, the UpdateStatus
// setting (GET /api/v1/admin/update): the version this server runs, the
// newest release the last look found and when, its page, why a newer
// version cannot be installed from here, and the last trouble. Check now
// looks at once (POST /api/v1/admin/update/check), which the server allows
// once a minute and never while the setting is off.
//
// Install is this admin's own: it asks first, then gets the bundle the
// plugin downloaded and checked (POST /api/v1/admin/update/bundle) and
// hands those bytes to Mattermost's own plugin upload (POST
// /api/v4/plugins) with this admin's session. Leaving the page in the
// middle is harmless: Mattermost has the bundle or it does not.
//
// Nothing here is saved, so it never calls onChange. The words are
// updateAdmin.ts's, which build/check-update.mjs holds.
export const AdminUpdate: React.FC<AdminCardProps> = ({id, label}) => {
    const [view, setView] = React.useState<UpdateView | null>(null);
    const [error, setError] = React.useState('');
    const [note, setNote] = React.useState('');
    const [loading, setLoading] = React.useState(true);
    const [checking, setChecking] = React.useState(false);
    const [install, dispatch] = React.useReducer(installNext, INSTALL_IDLE);
    const [installed, setInstalled] = React.useState('');
    const {statusRef, focus} = useCardFocus();

    const alive = React.useRef(true);
    React.useEffect(() => {
        alive.current = true;

        return () => {
            alive.current = false;
        };
    }, []);

    const settle = React.useCallback((answer: Promise<UpdateView>, done: string, stop: () => void) => {
        setError('');
        setNote('');

        answer.then((next) => {
            if (alive.current) {
                setView(next);
                setNote(done);
            }
        }).catch((reason: unknown) => {
            if (alive.current) {
                setError(reason instanceof Error ? reason.message : String(reason));
            }
        }).finally(() => {
            if (alive.current) {
                stop();
            }
        });
    }, []);

    const load = React.useCallback(() => {
        setLoading(true);
        settle(fetchAdminUpdate(), '', () => setLoading(false));
    }, [settle]);

    const check = React.useCallback(() => {
        setChecking(true);
        settle(checkAdminUpdate(), 'Looked just now.', () => setChecking(false));
    }, [settle]);

    React.useEffect(load, [load]);

    const version = view?.latest || '';

    const confirm = React.useCallback(() => {
        dispatch({kind: 'confirm'});
        setInstalled(version);

        // The bundle must be the version the admin was asked about; another
        // one, found meanwhile, is refused before anything is uploaded.
        fetchUpdateBundle(version).then((bundle) => {
            if (alive.current) {
                setInstalled(bundle.version || version);
                dispatch({kind: 'fetched'});
            }

            return uploadPluginBundle(bundle);
        }).then(() => {
            if (alive.current) {
                dispatch({kind: 'handed'});
            }
        }).catch((reason: unknown) => {
            if (alive.current) {
                dispatch({kind: 'failed', error: reason instanceof Error ? reason.message : String(reason)});

                // The server keeps why a bundle could not be had as the
                // card's trouble, and a newer version may have been found.
                load();
            }
        });
    }, [version, load]);

    const zone = viewerZone();
    let head: React.ReactNode;
    if (view === null) {
        head = loading ? 'Loading…' : 'How updates stand could not be loaded.';
    } else {
        head = updateHead(view);
    }
    const lines = view ? updateLines(view, (at) => postedWhen(at, zone), install.step === 'failed' ? install.error : undefined) : [];
    const page = view?.newer && view.url ? view.url : '';
    const offered = installOffered(view) || install.step !== 'idle';
    const buttons = installButtons(install);
    const stepText = installStepText(install, installed || version);
    const busy = loading || checking || buttons.disabled;

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead instant={true}>{head}</AdminHead>
            {lines.map((line) => (
                <p
                    key={line.text}
                    className={line.tone === 'error' ? 'vntv-admin-line vntv-notice-error' : `vntv-admin-line${line.tone === 'quiet' ? ' vntv-admin-quiet' : ''}`}
                >{line.text}</p>
            ))}
            {page ? (
                <p className='vntv-admin-line'>
                    {updatePageLinkable(page) ? (
                        <a
                            href={page}
                            target='_blank'
                            rel='noopener noreferrer'
                        >{'The release’s page'}</a>
                    ) : page}
                </p>
            ) : null}

            {offered && buttons.confirm ? installQuestion(version, view?.inVoice).map((line) => (
                <p
                    key={line}
                    className='vntv-admin-line'
                >{line}</p>
            )) : null}

            <div className='vntv-admin-actions'>
                {offered && buttons.install ? (
                    <AdminPill
                        tone='primary'
                        waiting={loading || checking}
                        working={buttons.disabled}
                        workingWords='Installing…'
                        onPress={() => dispatch({kind: 'press'})}
                    >{installButtonText(installButtonVersion(install, installed, version))}</AdminPill>
                ) : null}
                {offered && buttons.confirm ? (
                    <>
                        <AdminPill
                            tone='primary'
                            onPress={confirm}
                        >{'Install'}</AdminPill>
                        <AdminPill
                            onPress={() => dispatch({kind: 'cancel'})}
                        >{'Cancel'}</AdminPill>
                    </>
                ) : null}
                <AdminPill
                    waiting={busy}
                    off={view?.enabled === false}
                    title={view?.enabled === false ? 'Switch on Look for updates above and save first.' : undefined}
                    onPress={check}
                    working={checking}
                    workingWords='Looking…'
                >{'Check now'}</AdminPill>
                <AdminPill
                    waiting={busy}
                    onPress={load}
                >{'Refresh'}</AdminPill>
            </div>

            {stepText ? (
                <p
                    className={install.step === 'failed' ? 'vntv-admin-line vntv-notice-error' : 'vntv-admin-line'}
                    role='status'
                >{stepText}</p>
            ) : null}

            <AdminStatus
                ref={statusRef}
                note={note}
                error={error}
            />
        </AdminCard>
    );
};
