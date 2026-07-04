const MESSAGE_TYPE_JOINED = 0
const MESSAGE_TYPE_FULL = 1
const MESSAGE_TYPE_STARTED = 2
const MESSAGE_TYPE_GAME_STATE = 3
const MESSAGE_TYPE_GAME_END = 4
const MESSAGE_TYPE_READY = 5

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

type Paddle = {
    connId: number
    y: number
}

export function connectToGameServer(
    singleplayer: boolean,

    btnGameStart: HTMLButtonElement,
    btnPlayAgain: HTMLButtonElement,

    //
    onMessageJoined: (connId: number, connOrder: number, me: boolean) => void,
    onMessageStarted: () => void,
    onMessageReady: (connId: number) => void,
    onMessageGameState: (paddles: Paddle[], ballX: number, ballY: number) => void,
    onMessageGameEnd: (winner: number) => void,
) {
    const ws = new WebSocket(`ws://localhost:8080/game?${singleplayer ? "singleplayer=1" : ""}`)
    ws.binaryType = "arraybuffer"

    function sendStartMessage() {
        ws.send(new Uint8Array([MESSAGE_TYPE_START]))
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
        btnGameStart.addEventListener("click", sendStartMessage)
        btnPlayAgain.addEventListener("click", sendStartMessage)
        window.addEventListener("keydown", windowKeyDown)
        window.addEventListener("keyup", windowKeyUp)
    }

    ws.onclose = function () {
        btnGameStart.removeEventListener("click", sendStartMessage)
        btnPlayAgain.removeEventListener("click", sendStartMessage)
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
            case MESSAGE_TYPE_JOINED: {
                const connId = view.getUint32(1, true)
                const connOrder = view.getUint8(5)
                const me = view.getUint8(6) == 1
                onMessageJoined(connId, connOrder, me)
                break
            }
            case MESSAGE_TYPE_STARTED: {
                onMessageStarted()
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

                onMessageGameState(paddles, ballX, ballY)
                break
            }
            case MESSAGE_TYPE_GAME_END: {
                const view = new DataView(data)
                const winner = view.getUint32(1, true)
                onMessageGameEnd(winner)
                break
            }
            case MESSAGE_TYPE_READY: {
                const view = new DataView(data)
                const connId = view.getUint32(1, true)
                onMessageReady(connId)
                break
            }
        }
    }
}
