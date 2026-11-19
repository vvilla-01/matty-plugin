import type React from 'react';

// Whether a key belongs to an input method rather than to the form. Two
// signals, because Safari ends the composition BEFORE the keydown of the Enter
// that commits it: that keydown says isComposing false, and only its keyCode —
// 229, "the input method has it" — gives it away. Without this, confirming a
// Japanese or Chinese word saved the plan with it half written.
export function composing(event: React.KeyboardEvent): boolean {
    return event.nativeEvent.isComposing || event.keyCode === 229;
}
