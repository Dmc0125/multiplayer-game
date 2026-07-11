let WEBSOCKET_URL = "ws://localhost:8080/api/game"
if (import.meta.env.MODE === "production") {
    const [_, domain] = import.meta.env.SITE.split("https")
    WEBSOCKET_URL = `wss${domain}/api/game`
}

const MESSAGE_TYPE_JOINED = 0
const MESSAGE_TYPE_FULL = 1
const MESSAGE_TYPE_STARTED = 2
const MESSAGE_TYPE_GAME_STATE = 3
const MESSAGE_TYPE_GAME_END = 4
const MESSAGE_TYPE_READY = 5
const MESSAGE_TYPE_LOBBY_STATE = 6
const MESSAGE_TYPE_PLAYER_LEFT = 7
const MESSAGE_TYPE_SAVED = 8
const MESSAGE_TYPE_PONG = 9

const MESSAGE_TYPE_KEY = 100
const MESSAGE_TYPE_START = 101
const MESSAGE_TYPE_PING = 102

export const keyCodeMap: Record<string, number> = {
    ArrowUp: 0,
    ArrowDown: 1,
    " ": 2,
}

function encodeKeyMsg(keyCode: number, down: boolean): Uint8Array<ArrayBuffer> {
    const data = new Uint8Array(3)
    const view = new DataView(data.buffer)
    view.setUint8(0, MESSAGE_TYPE_KEY)
    view.setUint8(1, keyCode)
    view.setUint8(2, down ? 1 : 0)
    return data
}

export type Paddle = {
    connId: number
    y: number
}

export type Connection = {
    sendStartMessage: () => void
    sendKeyDown: (keyCode: number) => void
    sendKeyUp: (keyCode: number) => void
    sendPing: () => void
    close: () => void
    onMessageLobbyState?: (myConnId: number, otherConnId: number | undefined) => void
    onMessageJoined?: (connId: number) => void
    onMessagePlayerLeft?: () => void
    onMessageStarted?: () => void
    onMessageReady?: (left: boolean) => void
    onMessageGameState?: (
        paddleLeftY: number,
        paddleRightY: number,
        ballX: number,
        ballY: number,
    ) => void
    onMessageGameEnd?: (winnerLeft: boolean) => void
    onMessageSaved?: () => void
    onMessagePong?: (latencyMs: number) => void
}

export function connectToGameServer(singleplayer: boolean) {
    const ws = new WebSocket(`${WEBSOCKET_URL}?${singleplayer ? "singleplayer=1" : ""}`)
    ws.binaryType = "arraybuffer"

    function close() {
        ws.close()
    }

    function sendStartMessage() {
        if (ws.readyState === ws.OPEN) {
            ws.send(new Uint8Array([MESSAGE_TYPE_START]))
        }
    }

    function sendKeyDown(keyCode: number) {
        if (ws.readyState === ws.OPEN) {
            ws.send(encodeKeyMsg(keyCode, true))
        }
    }

    function sendKeyUp(keyCode: number) {
        if (ws.readyState === ws.OPEN) {
            ws.send(encodeKeyMsg(keyCode, false))
        }
    }

    const pingMessageIds = new Map<number, number>()
    let pingMessageId = 0

    function sendPing() {
        if (ws.readyState === ws.OPEN) {
            pingMessageIds.set(pingMessageId, Date.now())

            const data = new Uint8Array(5)
            const view = new DataView(data.buffer)
            view.setUint8(0, MESSAGE_TYPE_PING)
            view.setUint32(1, pingMessageId, true)
            ws.send(data)
        }
    }

    const connection: Connection = {
        sendStartMessage,
        sendKeyDown,
        sendKeyUp,
        sendPing,
        close,
    }

    ws.onmessage = function (event) {
        const data = event.data
        if (!(data instanceof ArrayBuffer)) {
            return
        }

        const view = new DataView(data)
        const messageType = view.getUint8(0)

        switch (messageType) {
            case MESSAGE_TYPE_PONG: {
                const messageId = view.getUint32(1, true)
                const sentAt = pingMessageIds.get(messageId)

                if (sentAt) {
                    const latencyMs = Date.now() - sentAt
                    connection.onMessagePong?.(latencyMs)
                    pingMessageIds.delete(messageId)
                }

                break
            }
            case MESSAGE_TYPE_LOBBY_STATE: {
                const myConnId = view.getUint32(1, true)
                let otherConnId: number | undefined
                if (view.byteLength > 5) {
                    otherConnId = view.getUint32(5, true)
                }
                connection.onMessageLobbyState?.(myConnId, otherConnId)
                break
            }
            case MESSAGE_TYPE_JOINED: {
                const connId = view.getUint32(1, true)
                connection.onMessageJoined?.(connId)
                break
            }
            case MESSAGE_TYPE_PLAYER_LEFT: {
                connection.onMessagePlayerLeft?.()
                break
            }
            case MESSAGE_TYPE_STARTED: {
                connection.onMessageStarted?.()
            }
            case MESSAGE_TYPE_GAME_STATE: {
                const view = new DataView(data)
                let offset = 1
                function decodef32(): number {
                    const f = view.getFloat32(offset, true)
                    offset += 4
                    return f
                }

                const paddleLeftY = decodef32()
                const paddleRightY = decodef32()
                const ballX = decodef32()
                const ballY = decodef32()

                connection.onMessageGameState?.(paddleLeftY, paddleRightY, ballX, ballY)
                break
            }
            case MESSAGE_TYPE_GAME_END: {
                const view = new DataView(data)
                const winner = view.getUint8(1)
                connection.onMessageGameEnd?.(winner === 1)
                break
            }
            case MESSAGE_TYPE_READY: {
                const view = new DataView(data)
                const left = view.getUint8(1)
                connection.onMessageReady?.(left === 1)
                break
            }
            case MESSAGE_TYPE_SAVED: {
                connection.onMessageSaved?.()
                break
            }
        }
    }

    return connection
}
