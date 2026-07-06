const MESSAGE_TYPE_JOINED = 0
const MESSAGE_TYPE_FULL = 1
const MESSAGE_TYPE_STARTED = 2
const MESSAGE_TYPE_GAME_STATE = 3
const MESSAGE_TYPE_GAME_END = 4
const MESSAGE_TYPE_READY = 5
const MESSAGE_TYPE_LOBBY_STATE = 6

const MESSAGE_TYPE_KEY = 100
const MESSAGE_TYPE_START = 101

const keyCodeMap: Record<string, number> = {
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
    sendPauseMessage: () => void
    close: () => void
    onMessageLobbyState?: (myConnId: number, otherConnId: number | undefined) => void
    onMessageJoined?: (connId: number) => void
    onMessageStarted?: () => void
    onMessageReady?: (connId: number) => void
    onMessageGameState?: (paddles: Paddle[], ballX: number, ballY: number) => void
    onMessageGameEnd?: (winner: number) => void
}

export function connectToGameServer(singleplayer: boolean) {
    const connection = {} as Connection

    const ws = new WebSocket(`ws://localhost:8080/game?${singleplayer ? "singleplayer=1" : ""}`)
    ws.binaryType = "arraybuffer"

    connection.close = () => {
        ws.close()
    }

    connection.sendStartMessage = function () {
        if (ws.readyState === ws.OPEN) {
            ws.send(new Uint8Array([MESSAGE_TYPE_START]))
        }
    }

    function windowKeyDown(event: KeyboardEvent) {
        const keyCode = keyCodeMap[event.key]
        if (keyCode !== undefined) {
            ws.send(encodeKeyMsg(keyCode, true))
        }
    }

    function windowKeyUp(event: KeyboardEvent) {
        const keyCode = keyCodeMap[event.key]
        if (keyCode !== undefined) {
            ws.send(encodeKeyMsg(keyCode, false))
        }
    }

    ws.onopen = function () {
        window.addEventListener("keydown", windowKeyDown)
        window.addEventListener("keyup", windowKeyUp)
    }

    ws.onclose = function () {
        window.removeEventListener("keydown", windowKeyDown)
        window.removeEventListener("keyup", windowKeyUp)
    }

    ws.onmessage = function (event) {
        const data = event.data
        if (!(data instanceof ArrayBuffer)) {
            return
        }

        const view = new DataView(data)
        const messageType = view.getUint8(0)

        switch (messageType) {
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
            case MESSAGE_TYPE_STARTED: {
                connection.onMessageStarted?.()
            }
            case MESSAGE_TYPE_GAME_STATE: {
                const view = new DataView(data)
                const paddles: Paddle[] = []

                let offset = 1
                function decodef32(): number {
                    const f = view.getFloat32(offset, true)
                    offset += 4
                    return f
                }

                for (let i = 0; i < 2; i++) {
                    const c = view.getUint32(offset, true)
                    offset += 4

                    const paddleY = decodef32()
                    paddles.push({
                        connId: c,
                        y: paddleY,
                    })
                }

                const ballX = decodef32()
                const ballY = decodef32()

                connection.onMessageGameState?.(paddles, ballX, ballY)
                break
            }
            case MESSAGE_TYPE_GAME_END: {
                const view = new DataView(data)
                const winner = view.getUint32(1, true)
                connection.onMessageGameEnd?.(winner)
                break
            }
            case MESSAGE_TYPE_READY: {
                const view = new DataView(data)
                const connId = view.getUint32(1, true)
                connection.onMessageReady?.(connId)
                break
            }
        }
    }

    return connection
}
