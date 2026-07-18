import { useEffect, useRef, useState, type RefObject } from "react"
import {
    connectToGameServer,
    keyCodeMap,
    type Connection,
    type GameEventCountdown,
    type GameEventState,
    type GameEventWinner,
} from "../websocket"

export const gameWidth = 800
export const gameHeight = 400

export const PADDLE_WIDTH = 10
export const PADDLE_HEIGHT = 100
export const BALL_RADIUS = 5

type PaddleDraw = {
    y: number
    left: boolean
    me: boolean
}

function drawGameState(
    ctx: CanvasRenderingContext2D,
    paddles: PaddleDraw[],
    ballX: number,
    ballY: number,
) {
    ctx.clearRect(0, 0, gameWidth, gameHeight)

    for (const paddle of paddles) {
        if (paddle.me) {
            ctx.fillStyle = "#35A7FF"
        } else {
            ctx.fillStyle = "#EF476F"
        }

        let paddleX: number
        if (paddle.left) {
            paddleX = 50
        } else {
            paddleX = gameWidth - 50 - PADDLE_WIDTH
        }

        const y = gameHeight - paddle.y - PADDLE_HEIGHT
        ctx.beginPath()
        ctx.roundRect(paddleX, y, PADDLE_WIDTH, PADDLE_HEIGHT, 10)
        ctx.fill()
    }

    ctx.fillStyle = "#000000"
    ctx.beginPath()
    const y = gameHeight - ballY
    ctx.arc(ballX, y, BALL_RADIUS, 0, 2 * Math.PI)
    ctx.fill()
}

type GameStatus =
    | "connecting"
    | "waiting"
    | "waiting-player-left"
    | "game-start"
    | { status: "countdown"; value: number }
    | "playing"
    | { status: "game-end"; saved: boolean; winnerLeft: boolean }

type MenuScreenProps = {
    status: GameStatus
    players: Players
    onStart: (() => void) | undefined
    onPlayAgain: (() => void) | undefined
    landscape?: boolean
}

function Announcer({ status, players, onStart, onPlayAgain, landscape }: MenuScreenProps) {
    if (status === "connecting") {
        return <p className="text-sm text-black">Connecting</p>
    }
    if (status === "waiting") {
        return <p className="text-sm text-black">Waiting for other player</p>
    }
    if (status === "waiting-player-left") {
        return (
            <div className="flex flex-col gap-4 items-center justify-center">
                <p className="text-sm text-black">Other player left</p>
                <p className="text-sm text-black">Waiting for other player</p>
            </div>
        )
    }
    if (status === "game-start") {
        return (
            <button
                className="btn-2 px-4 py-2 sm:px-8 sm:py-3"
                onClick={() => {
                    onStart?.()
                }}
            >
                Start {landscape ? "" : "(r)"}
            </button>
        )
    }
    if (status === "playing") {
        return <p className="text-lg text-black animate-announce">Go</p>
    }
    if (typeof status === "object") {
        switch (status.status) {
            case "countdown": {
                return (
                    <p key={status.value} className="text-lg text-black animate-announce">
                        {status.value}
                    </p>
                )
            }
            case "game-end": {
                const { saved, winnerLeft } = status
                let p: Player
                if (winnerLeft) {
                    p = players.left!
                } else {
                    p = players.right!
                }
                const textCn = p?.me ? "text-blue" : "text-pink"
                const text = p?.me ? "You won!" : "You lost!"

                return (
                    <div className="flex flex-col gap-4 items-center justify-center">
                        <p className={textCn}>{text}</p>
                        <button
                            className="btn-2 px-4 py-2 sm:px-8 sm:py-3"
                            disabled={!saved}
                            onClick={() => {
                                onPlayAgain?.()
                            }}
                        >
                            Play again {landscape ? "" : "(r)"}
                        </button>
                    </div>
                )
            }
        }
    }

    return <></>
}

type Player = {
    connId: number
    me: boolean
    score: number
    ready: boolean
}

type PlayerScoreProps = {
    players: Players
    singleplayer: boolean
    left: boolean
}

function PlayerScore({ singleplayer, players, left }: PlayerScoreProps) {
    let p: Player | undefined
    if (left) {
        p = players.left
    } else {
        p = players.right
    }

    const hideCn = p === undefined ? "opacity-0" : ""
    const me = p?.me === true
    const score = p?.score || 0
    const ready = p?.ready || false

    return (
        <div className="flex flex-col items-center gap-2">
            <p className={`text-xs uppercase ${hideCn}`}>{me ? "You" : "Enemy"}</p>
            <p className={`${me ? "text-blue" : "text-pink"} text-lg`}>{score}</p>
            {!singleplayer && (
                <p className={`text-xs ${ready ? "text-black" : "text-gray-400"} ${hideCn}`}>
                    Ready
                </p>
            )}
        </div>
    )
}

type Players = Record<"left" | "right", Player | undefined>

type GameConnection = {
    connection: RefObject<Connection>
    status: GameStatus
    connId: number | undefined
    players: Players
    latencyMs: number | undefined
}

function useGameConnection(
    singleplayer: boolean,
    draw: RefObject<(event: GameEventState) => void>,
): GameConnection {
    const connection = useRef<Connection>({} as Connection)
    const [status, setStatus] = useState<GameStatus>("connecting")
    const [connId, setConnId] = useState<number | undefined>(undefined)
    const [players, setPlayers] = useState<Players>({ left: undefined, right: undefined })
    const [latencyMs, setLatencyMs] = useState<number | undefined>(undefined)

    useEffect(() => {
        connection.current = connectToGameServer(singleplayer)

        connection.current.onMessageLobbyState = function (
            myConnId: number,
            otherConnId: number | undefined,
        ) {
            setConnId(myConnId)

            const me: Player = {
                connId: myConnId,
                me: true,
                score: 0,
                ready: false,
            }
            const newPlayers: Players = { left: undefined, right: undefined }

            if (otherConnId !== undefined) {
                // I'm right
                newPlayers.right = me
                newPlayers.left = {
                    connId: otherConnId,
                    me: false,
                    score: 0,
                    ready: false,
                }
                setStatus("game-start")
            } else {
                // I'm left
                newPlayers.left = me
                setStatus("waiting")
            }

            setPlayers(newPlayers)
        }

        connection.current.onMessageJoined = function (connId: number) {
            setPlayers((prev) => {
                prev.right = {
                    connId,
                    me: false,
                    score: 0,
                    ready: false,
                }
                return { ...prev }
            })
            setStatus("game-start")
        }

        connection.current.onMessageStarted = function () {
            setStatus("playing")
            setPlayers((prev) => {
                prev.right!.ready = false
                prev.left!.ready = false
                return { ...prev }
            })
        }

        connection.current.onMessageReady = function (left: boolean) {
            console.log("ready", left)
            setPlayers((prev) => {
                if (left && prev.left) {
                    prev.left.ready = true
                } else if (!left && prev.right) {
                    prev.right.ready = true
                }
                return { ...prev }
            })
        }

        connection.current.onMessageGameState = function (
            event: GameEventState | GameEventWinner | GameEventCountdown,
        ) {
            switch (event.type) {
                case "state": {
                    draw.current(event)
                    break
                }
                case "winner": {
                    setStatus({ status: "game-end", saved: false, winnerLeft: event.winnerLeft })
                    setPlayers((prev) => {
                        if (event.winnerLeft) {
                            prev.left!.score += 1
                        } else {
                            prev.right!.score += 1
                        }
                        return { ...prev }
                    })
                    break
                }
                case "countdown": {
                    if (event.countdown === 0) {
                        setStatus("playing")
                        setPlayers((prev) => {
                            prev.right!.ready = false
                            prev.left!.ready = false
                            return { ...prev }
                        })
                    } else {
                        setStatus({ status: "countdown", value: event.countdown })
                    }
                    break
                }
            }
        }

        connection.current.onMessageSaved = function () {
            setStatus((p) => {
                if (typeof p === "object" && p.status === "game-end") {
                    p.saved = true
                    return { ...p }
                }
                return p
            })
        }

        connection.current.onMessagePlayerLeft = function () {
            console.log("player left")
            setPlayers((prev) => {
                if (prev.left?.me === true) {
                    return {
                        ...prev,
                        right: undefined,
                    }
                } else {
                    // left player left, move me to left
                    return {
                        left: prev.right,
                        right: undefined,
                    }
                }
            })
            setStatus("waiting-player-left")
        }

        connection.current.onMessagePong = function (_latencyMs: number) {
            setLatencyMs(_latencyMs)
        }
        const pingInterval = setInterval(() => {
            connection.current.sendPing()
        }, 2000)

        return () => {
            clearInterval(pingInterval)
            connection.current.close()
        }
    }, [])

    return {
        connection,
        status,
        connId,
        players,
        latencyMs,
    }
}

type GameCanvasProps = {
    canvasRef: RefObject<HTMLCanvasElement | null>
}

function GameCanvas({ canvasRef }: GameCanvasProps) {
    function resizeCanvas(canvas: HTMLCanvasElement, ctx: CanvasRenderingContext2D) {
        const r = canvas.getBoundingClientRect()
        const dpr = window.devicePixelRatio || 1

        canvas.width = r.width * dpr
        canvas.height = r.height * dpr

        const scaleX = canvas.width / gameWidth
        const scaleY = canvas.height / gameHeight
        ctx.scale(scaleX, scaleY)
    }

    useEffect(() => {
        function r() {
            const canvas = canvasRef.current!
            const ctx = canvas.getContext("2d")!
            resizeCanvas(canvas, ctx)
        }
        r()

        window.addEventListener("resize", r)
        return () => {
            window.removeEventListener("resize", r)
        }
    }, [])

    return <canvas ref={canvasRef} className="w-full aspect-[2/1]"></canvas>
}

type GameLayoutProps = {
    singleplayer: boolean
    canvasRef: RefObject<HTMLCanvasElement | null>
    gameConnection: GameConnection
}

function PortraitLayout({ singleplayer, canvasRef, gameConnection }: GameLayoutProps) {
    const { status, players, latencyMs, connection } = gameConnection

    return (
        <div className="w-full max-w-[800px] mx-auto px-4">
            {/* score, ping, ready */}
            <header
                className="
                w-full px-4 py-4 bg-white 
                border-l-3 border-r-3 border-b-3 border-black rounded-b-lg flex items-center justify-center 
                "
            >
                {/* <div className="hidden sm:flex items-center gap-2"> */}
                {/*     <p className="text-sm">Ping: {latencyMs}ms</p> */}
                {/* </div> */}

                <div className="flex items-center gap-6">
                    <PlayerScore players={players} singleplayer={singleplayer} left={true} />
                    <p>vs</p>
                    <PlayerScore players={players} singleplayer={singleplayer} left={false} />
                </div>
            </header>

            <div className="w-full mt-10 border-3 border-black rounded-lg aspect-[2/1] bg-white relative">
                <GameCanvas canvasRef={canvasRef} />
                <div className="absolute inset-0 flex items-center justify-center">
                    <Announcer
                        status={status}
                        onStart={connection.current.sendStartMessage}
                        players={players}
                        onPlayAgain={connection.current.sendStartMessage}
                    />
                </div>
            </div>

            {/* tooltip */}
            <div className="w-full mt-10 flex items-center justify-center gap-2 flex-wrap">
                <p className="text-sm text-black">UP / DOWN to move </p>
                <p className="text-sm text-black">|</p>
                <p className="text-sm text-black">(r) to start</p>
            </div>
        </div>
    )
}

type LandscapeButtonProps = {
    sendKeyUp: (key: number) => void
    sendKeyDown: (key: number) => void
    keyCode: number
    action: string
}

function LandscapeButton({ sendKeyUp, sendKeyDown, keyCode, action }: LandscapeButtonProps) {
    return (
        <button
            className={`w-full aspect-[1/1] ${action === "UP" ? "bg-blue rounded-t-lg" : "bg-pink rounded-b-lg"}`}
            onTouchStart={() => sendKeyDown(keyCode)}
            onTouchEnd={() => sendKeyUp(keyCode)}
            onMouseDown={() => sendKeyDown(keyCode)}
            onMouseUp={() => sendKeyUp(keyCode)}
        >
            {action}
        </button>
    )
}

function LandscapeLayout({ canvasRef, gameConnection, singleplayer }: GameLayoutProps) {
    const { status, players, connection } = gameConnection
    const { sendKeyUp, sendKeyDown, sendStartMessage } = connection.current

    const buttonUp = (
        <LandscapeButton
            sendKeyUp={sendKeyUp}
            sendKeyDown={sendKeyDown}
            keyCode={keyCodeMap["ArrowUp"]}
            action="UP"
        />
    )
    const buttonDown = (
        <LandscapeButton
            sendKeyUp={sendKeyUp}
            sendKeyDown={sendKeyDown}
            keyCode={keyCodeMap["ArrowDown"]}
            action="DOWN"
        />
    )

    return (
        <div className="w-full h-[100vh] px-4 grid grid-cols-[20%_1fr_20%] gap-x-4 items-center">
            <div className="flex flex-col items-center justify-center rounded-lg border-3 border-black">
                {buttonUp}
                <div className="w-full h-[3px] bg-black"></div>
                {buttonDown}
            </div>

            <div className="w-full flex flex-col">
                <div className="w-full pb-4 flex items-center justify-center gap-4">
                    <PlayerScore players={players} singleplayer={singleplayer} left={true} />
                    <p>vs</p>
                    <PlayerScore players={players} singleplayer={singleplayer} left={false} />
                </div>
                <div className="w-full h-fit aspect-[2/1] border-3 border-black rounded-lg bg-white relative">
                    <GameCanvas canvasRef={canvasRef} />
                    <div className="absolute inset-0 flex items-center justify-center">
                        <Announcer
                            status={status}
                            onStart={sendStartMessage}
                            players={players}
                            onPlayAgain={sendStartMessage}
                            landscape={true}
                        />
                    </div>
                </div>
            </div>

            <div className="flex flex-col items-center justify-center rounded-lg border-3 border-black">
                {buttonUp}
                <div className="w-full h-[3px] bg-black"></div>
                {buttonDown}
            </div>
        </div>
    )
}

type GameProps = {
    singleplayer: boolean
}

export function Game({ singleplayer }: GameProps) {
    const canvasElement = useRef<HTMLCanvasElement>(null)
    const draw = useRef<(event: GameEventState) => void>(() => {})
    const gameConnection = useGameConnection(singleplayer, draw)
    const [landscape, setLandscape] = useState(false)

    const { connection, status, players } = gameConnection

    useEffect(() => {
        const landscapeQuery = window.matchMedia("(orientation: landscape) and (max-width: 1024px)")
        setLandscape(landscapeQuery.matches)

        function onOrientationChange(e: MediaQueryListEvent) {
            setLandscape(e.matches)
        }

        landscapeQuery.addEventListener("change", onOrientationChange)

        return () => {
            landscapeQuery.removeEventListener("change", onOrientationChange)
        }
    }, [])

    useEffect(() => {
        function onKeyUp(event: KeyboardEvent) {
            const k = keyCodeMap[event.key]
            if (k !== undefined) {
                connection.current.sendKeyUp(k)
            }
        }

        function onKeyDown(event: KeyboardEvent) {
            const k = keyCodeMap[event.key]
            if (k !== undefined) {
                connection.current.sendKeyDown(k)
            }
        }

        window.addEventListener("keydown", onKeyDown)
        window.addEventListener("keyup", onKeyUp)

        return () => {
            window.removeEventListener("keydown", onKeyDown)
            window.removeEventListener("keyup", onKeyUp)
        }
    }, [])

    useEffect(() => {
        draw.current = function (event: GameEventState) {
            const paddlesDraw: PaddleDraw[] = [
                {
                    y: event.leftPaddleY,
                    left: true,
                    me: players.left?.me === true,
                },
                {
                    y: event.rightPaddleY,
                    left: false,
                    me: players.right?.me === true,
                },
            ]

            const canvas = canvasElement.current!
            const ctx = canvas.getContext("2d")!
            drawGameState(ctx, paddlesDraw, event.ballX, event.ballY)
        }
    }, [players])

    useEffect(() => {
        function s(event: KeyboardEvent) {
            if (event.key === "r") {
                connection.current.sendStartMessage()
            }
        }

        if (status === "game-start" || typeof status === "object") {
            window.addEventListener("keydown", s)
        }

        return () => {
            window.removeEventListener("keydown", s)
        }
    }, [status])

    return (
        <>
            {landscape ? (
                <LandscapeLayout
                    singleplayer={singleplayer}
                    canvasRef={canvasElement}
                    gameConnection={gameConnection}
                />
            ) : (
                <PortraitLayout
                    singleplayer={singleplayer}
                    canvasRef={canvasElement}
                    gameConnection={gameConnection}
                />
            )}
        </>
    )
}
