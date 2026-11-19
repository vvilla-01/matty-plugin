import {exchangeWHEP} from './client';

// WhepPlayer mirrors the negotiation the Broadcast Box web player performs:
// audio and video receive-only transceivers, an offer with an ICE restart, and
// the offer posted immediately rather than waiting for gathering to finish.
// Broadcast Box's pion backend discovers the browser peer-reflexively, so
// waiting for candidates would only add latency.
//
// One of these per tile. Nothing is shared between them, so a failure in one
// grid tile cannot disturb another.
export class WhepPlayer {
    private connection: RTCPeerConnection | null = null;
    private inFlight: AbortController | null = null;
    private generation = 0;

    // peekLease is set only for a session opened to take a preview still (see
    // stills.ts), which tells the server to leave it out of the viewer count.
    async play(handle: string, video: HTMLVideoElement, onFailure?: (reason: string) => void, peekLease?: string): Promise<void> {
        this.stop();

        const generation = ++this.generation;
        const connection = new RTCPeerConnection();
        const inFlight = new AbortController();
        this.connection = connection;
        this.inFlight = inFlight;

        connection.addTransceiver('audio', {direction: 'recvonly'});
        connection.addTransceiver('video', {direction: 'recvonly'});

        const stream = new MediaStream();
        connection.ontrack = (event) => {
            stream.addTrack(event.track);
            video.srcObject = stream;
            event.track.onended = () => stream.removeTrack(event.track);
        };

        // Accepting the answer only means the negotiation was agreed. ICE can
        // still fail afterwards — a blocked UDP path, an answer with no usable
        // candidate — and without this the tile would sit black and silent with
        // nothing reported. 'disconnected' is deliberately not treated as a
        // failure: it recovers on its own most of the time.
        connection.onconnectionstatechange = () => {
            if (generation !== this.generation) {
                return;
            }
            if (connection.connectionState === 'failed') {
                onFailure?.('Lost the connection to this stream.');
            }
        };

        const offer = await connection.createOffer({iceRestart: true});

        // Ask for stereo Opus, exactly as the Broadcast Box player does.
        if (offer.sdp) {
            offer.sdp = offer.sdp.replace('useinbandfec=1', 'useinbandfec=1;stereo=1');
        }

        await connection.setLocalDescription(offer);

        const answer = await exchangeWHEP(handle, offer.sdp || '', inFlight.signal, peekLease);

        // A newer play() may have superseded this one while the request was in
        // flight; applying a stale answer would tear down the live session.
        if (generation !== this.generation) {
            return;
        }

        await connection.setRemoteDescription({type: 'answer', sdp: answer});
    }

    stop(): void {
        this.generation++;

        // Abandon a negotiation that has not come back yet. Without this, a
        // tile the viewer closed a moment after opening still costs the origin
        // a full SDP exchange, and with a grid people open and close tiles
        // constantly.
        this.inFlight?.abort();
        this.inFlight = null;

        if (this.connection) {
            this.connection.ontrack = null;
            this.connection.onconnectionstatechange = null;

            // close() alone leaves the received tracks live, which keeps a
            // decoder pinned per abandoned tile.
            this.connection.getReceivers().forEach((receiver) => receiver.track?.stop());
            this.connection.close();
            this.connection = null;
        }
    }
}
