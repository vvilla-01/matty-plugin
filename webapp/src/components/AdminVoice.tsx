import React from 'react';

import {fetchAdminVoice} from '../client';
import type {AdminVoice as VoiceView} from '../client';
import {postedWhen, viewerZone} from '../time';
import {voiceAdminLines} from '../voiceAdmin';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardFocus} from './adminParts';
import type {AdminCardProps} from './adminParts';

// System Console > Plugins > Matty > Mumble > Talking from Matty, the
// VoiceTalkStatus setting (GET /api/v1/admin/voice): whether the voice port
// is open and the addresses phones are given, or why not; whether the voice
// server is connected; how many people talk from Matty through this server
// now; the voice server's last refusal of one of them; and the lines of
// Channels to show that name no channel or more than one. Never who.
//
// Read-only: nothing here is saved, so it never calls onChange. The words
// are voiceAdmin.ts's, which build/check-voice.mjs holds.
export const AdminVoice: React.FC<AdminCardProps> = ({id, label}) => {
    const [view, setView] = React.useState<VoiceView | null>(null);
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

        fetchAdminVoice().then((answer) => {
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
        head = loading ? 'Loading…' : 'How talking from Matty stands could not be loaded.';
    } else {
        head = view.on ? 'Talking from Matty is on.' : 'Talking from Matty is off.';
    }
    const lines = view ? voiceAdminLines(view, (at) => postedWhen(at, zone)) : [];

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead>{head}</AdminHead>
            {lines.map((line) => (
                <p
                    key={line.text}
                    className={line.tone === 'error' ? 'vntv-admin-line vntv-notice-error' : `vntv-admin-line${line.tone === 'quiet' ? ' vntv-admin-quiet' : ''}`}
                >{line.text}</p>
            ))}

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
