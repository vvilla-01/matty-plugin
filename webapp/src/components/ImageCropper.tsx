import React from 'react';

import {AdminPill} from './adminParts';

// Choosing the part of a photo to use as a profile picture.
//
// A picture is shown in a circle wherever it appears, so a photo uploaded as
// it is gets whatever the middle of it happens to be — a shoulder, a wall, the
// top of a head. This puts the photo in a square with the circle drawn over
// it: drag the photo to move it, zoom to bring a face up to size, and what is
// inside the circle is what everybody sees.
//
// The crop is made HERE, in the browser, on a canvas, and only the result is
// sent: a 512 × 512 square. That is also what lets a big photo straight off a
// phone be used at all — the server's size limit applies to the square, not to
// the eight-megabyte original.
//
// The same, framed wide instead: a 24/7 channel's card picture fills a 16:9
// card, so it is chosen in a 16:9 frame and saved at 960 × 540.

type Props = {
    file: File;

    // The server's limit for the result. The square almost never comes near
    // it, but a PNG of a busy photo can, and then it is sent as a JPEG.
    maxBytes: number;

    busy?: boolean;
    onCancel: () => void;
    onCrop: (cropped: File) => void;

    // 'circle' by default: a square, with the circle it will be seen in drawn
    // over it. 'wide': a 16:9 frame for a card picture.
    shape?: 'circle' | 'wide';
};

// The frame the photo is chosen in, in CSS pixels, and what is saved.
const FRAMES = {
    circle: {stageW: 240, stageH: 240, outW: 512, outH: 512},
    wide: {stageW: 320, stageH: 180, outW: 960, outH: 540},
};

const MAX_ZOOM = 4;

// Arrow keys move this far, and ten times this with Shift.
const NUDGE = 6;

// Zoom per pixel of wheel: ln(1.08) / 100, so a mouse notch is eight per cent.
const WHEEL_RATE = Math.log(1.08) / 100;

type Offset = {x: number; y: number};

export const ImageCropper: React.FC<Props> = ({file, maxBytes, busy, onCancel, onCrop, shape = 'circle'}) => {
    const {stageW, stageH, outW, outH} = FRAMES[shape];

    const [url, setUrl] = React.useState('');
    const [size, setSize] = React.useState<{w: number; h: number} | null>(null);
    const [problem, setProblem] = React.useState('');
    const [zoom, setZoom] = React.useState(1);
    const [offset, setOffset] = React.useState<Offset>({x: 0, y: 0});
    const [dragging, setDragging] = React.useState(false);

    const imageRef = React.useRef<HTMLImageElement | null>(null);
    const stageRef = React.useRef<HTMLDivElement | null>(null);
    const dragRef = React.useRef<{pointer: number; from: Offset; x: number; y: number} | null>(null);

    // Focus goes to the photo as it opens, where the arrow keys move it: the
    // Choose a file that opened it has just gone or been disabled. A layout
    // effect, so it lands before the card around it looks for somewhere to
    // send focus that was left with nowhere to go.
    React.useLayoutEffect(() => {
        stageRef.current?.focus();
    }, []);

    // Readable from the wheel listener, which is added once.
    const stateRef = React.useRef({zoom, offset, size});
    stateRef.current = {zoom, offset, size};

    React.useEffect(() => {
        const address = URL.createObjectURL(file);
        setUrl(address);
        setSize(null);
        setProblem('');
        setZoom(1);

        return () => URL.revokeObjectURL(address);
    }, [file]);

    // Scale that makes the photo just cover the frame, at zoom 1.
    const coverFor = React.useCallback((dims: {w: number; h: number}): number => Math.max(stageW / dims.w, stageH / dims.h), [stageW, stageH]);
    const cover = size ? coverFor(size) : 1;

    // Kept covering the frame: the photo can be moved about under it but never
    // so far that an edge of it shows inside.
    const clamp = React.useCallback((next: Offset, scale: number, dims: {w: number; h: number}): Offset => ({
        x: Math.min(0, Math.max(stageW - (dims.w * scale), next.x)),
        y: Math.min(0, Math.max(stageH - (dims.h * scale), next.y)),
    }), [stageW, stageH]);

    const loaded = () => {
        const image = imageRef.current;
        if (!image) {
            return;
        }

        // An SVG with no size of its own reports none; treat it as the size
        // it is being saved at.
        const w = image.naturalWidth || outW;
        const h = image.naturalHeight || outH;
        const scale = coverFor({w, h});

        setSize({w, h});
        setOffset({x: (stageW - (w * scale)) / 2, y: (stageH - (h * scale)) / 2});
    };

    // Zooming keeps whatever is in the middle of the frame in the middle.
    const zoomTo = React.useCallback((next: number) => {
        const {zoom: was, offset: at, size: dims} = stateRef.current;
        if (!dims) {
            return;
        }

        const target = Math.min(MAX_ZOOM, Math.max(1, next));
        const base = coverFor(dims);
        const before = base * was;
        const after = base * target;

        const middleX = ((stageW / 2) - at.x) / before;
        const middleY = ((stageH / 2) - at.y) / before;

        setZoom(target);
        setOffset(clamp({x: (stageW / 2) - (middleX * after), y: (stageH / 2) - (middleY * after)}, after, dims));
    }, [clamp, coverFor, stageW, stageH]);

    // The wheel zooms. Added by hand rather than through React, whose wheel
    // listener is passive and so cannot stop the console page scrolling at the
    // same time.
    React.useEffect(() => {
        const stage = stageRef.current;
        if (!stage) {
            return undefined;
        }

        // In proportion to how far the wheel went, not a fixed step per event:
        // a trackpad sends dozens of tiny ones per gesture, and a fixed step
        // each took the zoom from one end to the other in a flick. A mouse
        // notch — about a hundred pixels — is still about eight per cent.
        // Sideways scrolling is not zooming at all.
        const wheel = (event: WheelEvent) => {
            event.preventDefault();
            if (event.deltaY === 0) {
                return;
            }

            const lines = event.deltaMode === 1 ? 16 : 1;
            const pages = event.deltaMode === 2 ? stageH : 1;
            const pixels = event.deltaY * lines * pages;

            zoomTo(stateRef.current.zoom * Math.exp(-pixels * WHEEL_RATE));
        };

        stage.addEventListener('wheel', wheel, {passive: false});

        return () => stage.removeEventListener('wheel', wheel);
    }, [zoomTo]);

    const scale = cover * zoom;

    const moveBy = (dx: number, dy: number) => {
        if (size) {
            setOffset((at) => clamp({x: at.x + dx, y: at.y + dy}, scale, size));
        }
    };

    const crop = async () => {
        const image = imageRef.current;
        if (!image || !size) {
            return;
        }

        // The whole picture, drawn where the preview has it and scaled up from
        // the preview's frame to the saved one; the canvas keeps what falls
        // inside. Not a source rectangle cut out in the picture's own pixels:
        // an SVG with only a viewBox reports a stand-in size (150 × 150 in
        // Chromium) but is DRAWN at whatever size it is asked for, so a cut
        // measured in the stand-in pixels landed on the wrong part of it.
        const k = outW / stageW;

        const draw = (background?: string): HTMLCanvasElement => {
            const canvas = document.createElement('canvas');
            canvas.width = outW;
            canvas.height = outH;
            const context = canvas.getContext('2d');
            if (!context) {
                throw new Error('This browser could not make the picture.');
            }

            if (background) {
                context.fillStyle = background;
                context.fillRect(0, 0, outW, outH);
            }

            context.imageSmoothingEnabled = true;
            context.imageSmoothingQuality = 'high';
            context.drawImage(image, offset.x * k, offset.y * k, size.w * scale * k, size.h * scale * k);

            return canvas;
        };

        const encode = (canvas: HTMLCanvasElement, type: string): Promise<Blob> => new Promise((resolve, reject) => {
            canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error('This browser could not make the picture.'))), type, 0.9);
        });

        try {
            // A photo stays a JPEG. Anything that might be see-through is a PNG,
            // unless that comes out over the limit — then a JPEG on black,
            // which is the colour it is shown against anyway.
            let type = file.type === 'image/jpeg' ? 'image/jpeg' : 'image/png';
            let blob = await encode(draw(type === 'image/jpeg' ? '#000000' : undefined), type);

            if (blob.size > maxBytes && type === 'image/png') {
                type = 'image/jpeg';
                blob = await encode(draw('#000000'), type);
            }

            if (blob.size > maxBytes) {
                throw new Error('That picture is still too large once cropped.');
            }

            const base = file.name.replace(/\.[^.]*$/, '') || 'picture';
            onCrop(new File([blob], `${base}.${type === 'image/jpeg' ? 'jpg' : 'png'}`, {type}));
        } catch (reason) {
            setProblem(reason instanceof Error ? reason.message : String(reason));
        }
    };

    return (
        <div className='vntv-crop'>
            <div
                ref={stageRef}
                className='vntv-crop-stage'
                data-shape={shape}
                data-dragging={dragging ? 'true' : undefined}
                tabIndex={0}
                role='group'
                aria-label='Choose the part of the picture to use. Arrow keys move it, plus and minus zoom.'
                onPointerDown={(event) => {
                    if (!size || event.button !== 0) {
                        return;
                    }

                    event.currentTarget.setPointerCapture(event.pointerId);
                    dragRef.current = {pointer: event.pointerId, from: offset, x: event.clientX, y: event.clientY};
                    setDragging(true);
                }}
                onPointerMove={(event) => {
                    const drag = dragRef.current;
                    if (!drag || drag.pointer !== event.pointerId || !size) {
                        return;
                    }

                    setOffset(clamp({
                        x: drag.from.x + (event.clientX - drag.x),
                        y: drag.from.y + (event.clientY - drag.y),
                    }, scale, size));
                }}
                onPointerUp={() => {
                    dragRef.current = null;
                    setDragging(false);
                }}
                onPointerCancel={() => {
                    dragRef.current = null;
                    setDragging(false);
                }}
                onKeyDown={(event) => {
                    const step = event.shiftKey ? NUDGE * 10 : NUDGE;
                    const moves: Record<string, [number, number]> = {
                        ArrowLeft: [step, 0],
                        ArrowRight: [-step, 0],
                        ArrowUp: [0, step],
                        ArrowDown: [0, -step],
                    };

                    if (moves[event.key]) {
                        event.preventDefault();
                        moveBy(...moves[event.key]);
                    } else if (event.key === '+' || event.key === '=') {
                        event.preventDefault();
                        zoomTo(zoom + 0.2);
                    } else if (event.key === '-' || event.key === '_') {
                        event.preventDefault();
                        zoomTo(zoom - 0.2);
                    }
                }}
            >
                {url !== '' && (
                    <img
                        ref={imageRef}
                        className='vntv-crop-image'
                        src={url}
                        alt=''
                        draggable={false}
                        onLoad={loaded}
                        onError={() => setProblem('That file could not be opened as a picture.')}
                        style={size ? {
                            width: size.w * scale,
                            height: size.h * scale,
                            transform: `translate(${offset.x}px, ${offset.y}px)`,
                        } : {visibility: 'hidden'}}
                    />
                )}
                <span
                    className='vntv-crop-ring'
                    aria-hidden={true}
                />
            </div>

            <div className='vntv-crop-side'>
                <label className='vntv-admin-line vntv-crop-zoom'>
                    <span>{'Zoom'}</span>
                    <input
                        type='range'
                        min={1}
                        max={MAX_ZOOM}
                        step={0.01}
                        value={zoom}
                        disabled={!size}
                        onChange={(event) => zoomTo(Number(event.target.value))}
                    />
                </label>
                <p className='vntv-admin-line vntv-admin-quiet'>
                    {shape === 'wide' ?
                        'Drag the picture to move it, and zoom in on what matters. What is inside the frame is what everybody sees on the card.' :
                        'Drag the picture to move it, and zoom to bring a face up to size. What is inside the circle is what everybody sees.'}
                </p>

                {problem !== '' && <p className='vntv-admin-line vntv-notice-error'>{problem}</p>}

                <div className='vntv-admin-actions'>
                    <AdminPill
                        tone='primary'
                        off={!size}
                        waiting={busy}
                        working={busy}
                        workingWords='Saving…'
                        onPress={() => {
                            void crop();
                        }}
                    >{'Use this'}</AdminPill>
                    <AdminPill
                        waiting={busy}
                        onPress={onCancel}
                    >{'Cancel'}</AdminPill>
                </div>
            </div>
        </div>
    );
};
