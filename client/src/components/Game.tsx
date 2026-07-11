import { useEffect, useRef, useState, type RefObject } from "react"
import { connectToGameServer, keyCodeMap, type Connection } from "../websocket"

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
            ctx.fillStyle = "#e8dab2"
        } else {
            ctx.fillStyle = "#dd6e42"
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

    ctx.fillStyle = "#e8dab2"
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
    | { status: "game-end"; saved: boolean; winnerLeft: boolean }
    | "playing"

type MenuScreenProps = {
    status: GameStatus
    players: Players
    onStart: (() => void) | undefined
    onPlayAgain: (() => void) | undefined
}

function MenuScreen({ status, players, onStart, onPlayAgain }: MenuScreenProps) {
    if (status === "connecting") {
        return <p className="text-white">Connecting</p>
    }
    if (status === "waiting") {
        return <p className="text-white">Waiting for other player</p>
    }
    if (status === "waiting-player-left") {
        return (
            <div className="flex flex-col gap-4 items-center justify-center">
                <p className="text-white">Other player left</p>
                <p className="text-white">Waiting for other player</p>
            </div>
        )
    }
    if (status === "game-start") {
        return (
            <button
                className="btn"
                onClick={() => {
                    onStart?.()
                }}
            >
                Start (r)
            </button>
        )
    }
    if (typeof status === "object") {
        const { saved, winnerLeft } = status
        let p: Player
        if (winnerLeft) {
            p = players.left!
        } else {
            p = players.right!
        }
        const text = p?.me ? "You won!" : "You lost!"

        return (
            <div className="flex flex-col gap-4 items-center justify-center">
                <p className="text-light-2">{text}</p>
                <button
                    className="btn"
                    disabled={!saved}
                    onClick={() => {
                        onPlayAgain?.()
                    }}
                >
                    Play again (r)
                </button>
            </div>
        )
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
        <div
            className={`w-[40%] p-4 bg-dark-3 flex flex-col border-3 border-dark-2 ${left ? "" : "text-right"}`}
        >
            <p className={`text-xs text-light-2 uppercase ${hideCn}`}>{me ? "You" : "Enemy"}</p>
            <p
                className={`mt-3 text-4xl font-medium ${me ? "text-yellow" : "text-orange"} ${hideCn}`}
            >
                {score}
            </p>
            {!singleplayer && (
                <p className={`mt-2 text-xs ${ready ? "text-green-700" : "text-dark-1"} ${hideCn}`}>
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

function useGameConnection(singleplayer: boolean): GameConnection {
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
            setPlayers((prev) => {
                if (left) {
                    prev.left!.ready = true
                } else {
                    prev.right!.ready = true
                }
                return { ...prev }
            })
        }

        connection.current.onMessageGameEnd = function (winnerLeft: boolean) {
            setStatus({ status: "game-end", saved: false, winnerLeft })
            setPlayers((prev) => {
                if (winnerLeft) {
                    prev.left!.score += 1
                } else {
                    prev.right!.score += 1
                }
                return { ...prev }
            })
        }

        connection.current.onMessageSaved = function () {
            setStatus((p) => {
                if (typeof p === "object") {
                    p.saved = true
                    return { ...p }
                }
                return p
            })
        }

        connection.current.onMessagePlayerLeft = function () {
            setPlayers((prev) => {
                if (prev.left?.me === true) {
                    prev.right = undefined
                } else {
                    prev.left = undefined
                }
                return { ...prev }
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
        ctx.setTransform(1, 0, 0, 1, 0, 0)

        const r = canvas.getBoundingClientRect()
        canvas.width = r.width
        canvas.height = r.height

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

    return (
        <canvas
            ref={canvasRef}
            className="w-full aspect-[2/1] border-3 border-orange bg-dark-3 crt-scanlines"
        ></canvas>
    )
}

type GameLayoutProps = {
    singleplayer: boolean
    canvasRef: RefObject<HTMLCanvasElement | null>
    gameConnection: GameConnection
}

function PortraitLayout({ singleplayer, canvasRef, gameConnection }: GameLayoutProps) {
    const { status, players, latencyMs, connection } = gameConnection

    return (
        <div className="pt-10 px-2 sm:px-0 flex flex-col items-center justify-center">
            <h1 className="text-light-1 text-xl sm:text-4xl font-bold">
                {singleplayer ? "Singleplayer" : "Multiplayer"}
            </h1>

            <div className="w-full max-w-[800px] mt-10">
                <div className="w-full max-w-[800px] flex items-center justify-between">
                    <PlayerScore players={players} singleplayer={singleplayer} left={true} />

                    <div className="flex flex-col items-center gap-2 text-xs">
                        <p className="text-yellow">Connected</p>
                        <p className="text-light-2">Ping {latencyMs}ms</p>
                    </div>

                    <PlayerScore players={players} singleplayer={singleplayer} left={false} />
                </div>

                <div className="relative w-full max-w-[800px] mt-6 aspect-[2/1] mx-auto canvas-wrapper">
                    <GameCanvas canvasRef={canvasRef} />
                    <div className="absolute inset-0 flex items-center justify-center">
                        <MenuScreen
                            status={status}
                            onStart={connection.current.sendStartMessage}
                            players={players}
                            onPlayAgain={connection.current.sendStartMessage}
                        />
                    </div>
                </div>

                <div className="flex gap-8 items-center w-fit mx-auto mt-6">
                    <div className="flex flex-col items-center gap-2">
                        <div className="bg-dark-2 w-fit p-2 rounded">
                            <svg className="size-4 text-light-2">
                                <use href="/icons.svg#arrow-up"></use>
                            </svg>
                        </div>
                        <p className="text-xs text-light-2">up</p>
                    </div>
                    <div className="flex flex-col items-center gap-2">
                        <div className="bg-dark-2 w-fit p-2 rounded">
                            <svg className="size-4 text-light-2 rotate-180">
                                <use href="/icons.svg#arrow-up"></use>
                            </svg>
                        </div>
                        <p className="text-xs text-light-2">down</p>
                    </div>
                </div>
            </div>
        </div>
    )
}

function LandscapeLayout({ canvasRef, gameConnection }: GameLayoutProps) {
    const { status, players, connection } = gameConnection
    const { sendKeyUp, sendKeyDown, sendStartMessage } = connection.current

    return (
        <div className="w-full h-[100vh] grid grid-cols-[15%_1fr_15%] items-center bg-black">
            <button
                className="h-full bg-dark-3 text-yellow flex items-center justify-center"
                onMouseDown={() => sendKeyDown(keyCodeMap["ArrowUp"])}
                onMouseUp={() => sendKeyUp(keyCodeMap["ArrowUp"])}
                onTouchStart={() => sendKeyDown(keyCodeMap["ArrowUp"])}
                onTouchEnd={() => sendKeyUp(keyCodeMap["ArrowUp"])}
            >
                UP
            </button>
            <div className="relative w-full aspect-[2/1] mx-auto">
                <GameCanvas canvasRef={canvasRef} />
                <div className="absolute inset-0 flex items-center justify-center">
                    <MenuScreen
                        status={status}
                        onStart={sendStartMessage}
                        players={players}
                        onPlayAgain={sendStartMessage}
                    />
                </div>
            </div>
            <button
                className="h-full bg-dark-3 text-yellow flex items-center justify-center"
                onMouseDown={() => sendKeyDown(keyCodeMap["ArrowDown"])}
                onMouseUp={() => sendKeyUp(keyCodeMap["ArrowDown"])}
                onTouchStart={() => sendKeyDown(keyCodeMap["ArrowDown"])}
                onTouchEnd={() => sendKeyUp(keyCodeMap["ArrowDown"])}
            >
                DOWN
            </button>
        </div>
    )
}

type GameProps = {
    singleplayer: boolean
}

export function Game({ singleplayer }: GameProps) {
    const canvasElement = useRef<HTMLCanvasElement>(null)
    const gameConnection = useGameConnection(singleplayer)
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
        connection.current.onMessageGameState = function (
            paddleLeftY: number,
            paddleRightY: number,
            ballX: number,
            ballY: number,
        ) {
            const paddlesDraw: PaddleDraw[] = [
                {
                    y: paddleLeftY,
                    left: true,
                    me: players.left?.me === true,
                },
                {
                    y: paddleRightY,
                    left: false,
                    me: players.right?.me === true,
                },
            ]
            const canvas = canvasElement.current!
            const ctx = canvas.getContext("2d")!
            drawGameState(ctx, paddlesDraw, ballX, ballY)
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
