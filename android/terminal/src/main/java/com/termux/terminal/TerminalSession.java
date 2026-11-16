/*
 * Adapted from Termux's TerminalSession (Apache License 2.0, derived from
 * jackpal/Android-Terminal-Emulator). Instead of forking a local process over a
 * pty, this session is fed bytes from a remote terminal (the agyc daemon's
 * tmux attachment over a WebSocket) and sends user input back through
 * {@link RemoteIO}.
 */
package com.termux.terminal;

import android.annotation.SuppressLint;
import android.os.Handler;
import android.os.Looper;
import android.os.Message;

import java.nio.charset.StandardCharsets;
import java.util.UUID;

public final class TerminalSession extends TerminalOutput {

    /** Transport to the remote terminal. Implementations may be called from the main thread. */
    public interface RemoteIO {
        void send(byte[] data, int offset, int count);

        void resize(int columns, int rows);

        void close();
    }

    private static final int MSG_NEW_INPUT = 1;
    private static final int MSG_PROCESS_EXITED = 4;

    public final String mHandle = UUID.randomUUID().toString();

    TerminalEmulator mEmulator;

    /** Bytes received from the remote side, drained on the main thread into the emulator. */
    final ByteQueue mProcessToTerminalIOQueue = new ByteQueue(256 * 1024);

    private final byte[] mUtf8InputBuffer = new byte[5];

    TerminalSessionClient mClient;

    private volatile boolean mRunning = true;
    private int mExitStatus;
    private final RemoteIO mIO;
    private final Integer mTranscriptRows;

    public String mSessionName;

    final Handler mMainThreadHandler = new MainThreadHandler();

    private static final String LOG_TAG = "TerminalSession";

    public TerminalSession(Integer transcriptRows, TerminalSessionClient client, RemoteIO io) {
        this.mTranscriptRows = transcriptRows;
        this.mClient = client;
        this.mIO = io;
    }

    public void updateTerminalSessionClient(TerminalSessionClient client) {
        mClient = client;
        if (mEmulator != null) mEmulator.updateTerminalSessionClient(client);
    }

    /** Inform the remote pty of the new size and reflow or initialize the emulator. */
    public void updateSize(int columns, int rows, int cellWidthPixels, int cellHeightPixels) {
        if (mEmulator == null) {
            initializeEmulator(columns, rows, cellWidthPixels, cellHeightPixels);
        } else {
            mEmulator.resize(columns, rows, cellWidthPixels, cellHeightPixels);
        }
        if (mIO != null) mIO.resize(columns, rows);
    }

    public String getTitle() {
        return (mEmulator == null) ? null : mEmulator.getTitle();
    }

    public void initializeEmulator(int columns, int rows, int cellWidthPixels, int cellHeightPixels) {
        mEmulator = new TerminalEmulator(this, columns, rows, cellWidthPixels, cellHeightPixels, mTranscriptRows, mClient);
        // Bytes may have arrived before the view was laid out.
        mMainThreadHandler.sendEmptyMessage(MSG_NEW_INPUT);
    }

    /** Feed bytes received from the remote terminal (any thread). */
    public void feed(byte[] data, int offset, int count) {
        if (!mRunning) return;
        if (mProcessToTerminalIOQueue.write(data, offset, count)) {
            mMainThreadHandler.sendEmptyMessage(MSG_NEW_INPUT);
        }
    }

    /** Called when the remote side closed (any thread). */
    public void remoteExited(int exitStatus) {
        mMainThreadHandler.sendMessage(mMainThreadHandler.obtainMessage(MSG_PROCESS_EXITED, exitStatus));
    }

    /** Write data to the remote terminal. */
    @Override
    public void write(byte[] data, int offset, int count) {
        if (mRunning && mIO != null) mIO.send(data, offset, count);
    }

    /** Write the Unicode code point to the terminal encoded in UTF-8. */
    public void writeCodePoint(boolean prependEscape, int codePoint) {
        if (codePoint > 1114111 || (codePoint >= 0xD800 && codePoint <= 0xDFFF)) {
            throw new IllegalArgumentException("Invalid code point: " + codePoint);
        }
        int bufferPosition = 0;
        if (prependEscape) mUtf8InputBuffer[bufferPosition++] = 27;
        if (codePoint <= 0b1111111) {
            mUtf8InputBuffer[bufferPosition++] = (byte) codePoint;
        } else if (codePoint <= 0b11111111111) {
            mUtf8InputBuffer[bufferPosition++] = (byte) (0b11000000 | (codePoint >> 6));
            mUtf8InputBuffer[bufferPosition++] = (byte) (0b10000000 | (codePoint & 0b111111));
        } else if (codePoint <= 0b1111111111111111) {
            mUtf8InputBuffer[bufferPosition++] = (byte) (0b11100000 | (codePoint >> 12));
            mUtf8InputBuffer[bufferPosition++] = (byte) (0b10000000 | ((codePoint >> 6) & 0b111111));
            mUtf8InputBuffer[bufferPosition++] = (byte) (0b10000000 | (codePoint & 0b111111));
        } else {
            mUtf8InputBuffer[bufferPosition++] = (byte) (0b11110000 | (codePoint >> 18));
            mUtf8InputBuffer[bufferPosition++] = (byte) (0b10000000 | ((codePoint >> 12) & 0b111111));
            mUtf8InputBuffer[bufferPosition++] = (byte) (0b10000000 | ((codePoint >> 6) & 0b111111));
            mUtf8InputBuffer[bufferPosition++] = (byte) (0b10000000 | (codePoint & 0b111111));
        }
        write(mUtf8InputBuffer, 0, bufferPosition);
    }

    /** Send a string as UTF-8. */
    public void writeString(String s) {
        byte[] b = s.getBytes(StandardCharsets.UTF_8);
        write(b, 0, b.length);
    }

    public TerminalEmulator getEmulator() {
        return mEmulator;
    }

    protected void notifyScreenUpdate() {
        if (mClient != null) mClient.onTextChanged(this);
    }

    public void reset() {
        if (mEmulator != null) {
            mEmulator.reset();
            notifyScreenUpdate();
        }
    }

    /** Detach from the remote terminal (it keeps running on the host). */
    public void finishIfRunning() {
        if (mRunning && mIO != null) mIO.close();
    }

    @Override
    public void titleChanged(String oldTitle, String newTitle) {
        if (mClient != null) mClient.onTitleChanged(this);
    }

    public synchronized boolean isRunning() {
        return mRunning;
    }

    public synchronized int getExitStatus() {
        return mExitStatus;
    }

    @Override
    public void onCopyTextToClipboard(String text) {
        if (mClient != null) mClient.onCopyTextToClipboard(this, text);
    }

    @Override
    public void onPasteTextFromClipboard() {
        if (mClient != null) mClient.onPasteTextFromClipboard(this);
    }

    @Override
    public void onBell() {
        if (mClient != null) mClient.onBell(this);
    }

    @Override
    public void onColorsChanged() {
        if (mClient != null) mClient.onColorsChanged(this);
    }

    public int getPid() {
        return mRunning ? 1 : -1;
    }

    public String getCwd() {
        return null;
    }

    @SuppressLint("HandlerLeak")
    class MainThreadHandler extends Handler {

        final byte[] mReceiveBuffer = new byte[64 * 1024];

        MainThreadHandler() {
            super(Looper.getMainLooper());
        }

        @Override
        public void handleMessage(Message msg) {
            if (mEmulator != null) {
                int bytesRead;
                boolean any = false;
                while ((bytesRead = mProcessToTerminalIOQueue.read(mReceiveBuffer, false)) > 0) {
                    mEmulator.append(mReceiveBuffer, bytesRead);
                    any = true;
                }
                if (any) notifyScreenUpdate();
            }
            if (msg.what == MSG_PROCESS_EXITED) {
                synchronized (TerminalSession.this) {
                    if (!mRunning) return;
                    mRunning = false;
                    mExitStatus = (msg.obj instanceof Integer) ? (Integer) msg.obj : 0;
                }
                mProcessToTerminalIOQueue.close();
                if (mEmulator != null) {
                    byte[] bytesToWrite = "\r\n\u001b[2m[disconnected]\u001b[0m".getBytes(StandardCharsets.UTF_8);
                    mEmulator.append(bytesToWrite, bytesToWrite.length);
                    notifyScreenUpdate();
                }
                if (mClient != null) mClient.onSessionFinished(TerminalSession.this);
            }
        }
    }
}
