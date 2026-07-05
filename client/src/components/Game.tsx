import { useEffect, useRef, useState } from "react"
import { connectToGameServer, type Paddle, type Connection } from "../websocket"

export const gameWidth = 800
export const gameHeight = 400

function resizeCanvas(canvas: HTMLCanvasElement, ctx: CanvasRenderingContext2D) {
    ctx.setTransform(1, 0, 0, 1, 0, 0)

    const r = canvas.getBoundingClientRect()
    canvas.width = r.width
    canvas.height = r.height

    const scaleX = canvas.width / gameWidth
    const scaleY = canvas.height / gameHeight
    ctx.scale(scaleX, scaleY)
}

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

type GameStatus = "connecting" | "waiting" | "game-start" | "game-end" | "playing"

type MenuScreenProps = {
    status: GameStatus
    winner: number | undefined
    players: Record<number, Player>
    onStart: (() => void) | undefined
    onPlayAgain: (() => void) | undefined
}

function MenuScreen({ status, winner, players, onStart, onPlayAgain }: MenuScreenProps) {
    switch (status) {
        case "connecting":
            return <p className="text-white">Connecting</p>
        case "waiting":
            return <p className="text-white">Waiting for other player</p>
        case "game-start":
            return (
                <button
                    className="btn"
                    onClick={() => {
                        onStart?.()
                    }}
                >
                    Start
                </button>
            )
        case "game-end":
            let text = ""
            if (winner !== undefined) {
                const p = players[winner]
                if (p?.me) {
                    text = "You won!"
                } else {
                    text = "You lost!"
                }
            }

            return (
                <div className="flex flex-col gap-4 items-center justify-center">
                    <p className="text-light">{text}</p>
                    <button
                        className="btn"
                        onClick={() => {
                            onPlayAgain?.()
                        }}
                    >
                        Play again (r)
                    </button>
                </div>
            )
        case "playing":
            return <></>
    }
}

type Player = {
    connId: number
    me: boolean
    score: number
    ready: boolean
}

type PlayerScoreProps = {
    connId: number | undefined
    players: Record<number, Player>
}

function PlayerScore({ connId, players }: PlayerScoreProps) {
    if (connId === undefined) {
        return (
            <div className="w-[40%] p-4 bg-dark flex flex-col border-3 border-dark-2 pointer-events-none">
                <p className="text-xs text-light opacity-0">Player 1</p>
                <p className="mt-2 text-lg font-medium opacity-0">0</p>
                <p className="text-xs text-light opacity-0">Ready</p>
            </div>
        )
    }

    const { me, score, ready } = players[connId]
    return (
        <div className="w-[40%] p-4 bg-dark flex flex-col border-3 border-dark-2">
            <p className="text-xs text-light">Player 1</p>
            <p className={`mt-2 text-lg font-medium ${me ? "text-yellow" : "text-orange"}`}>
                {score}
            </p>
            <p className={`text-xs ${ready ? "text-green-500" : "text-light"}`}>Ready</p>
        </div>
    )
}

type GameProps = {
    singleplayer: boolean
}

export function Game({ singleplayer }: GameProps) {
    const canvasElement = useRef<HTMLCanvasElement>(null)
    const connection = useRef<Connection>({} as Connection)
    const [status, setStatus] = useState<GameStatus>("connecting")
    const [connId, setConnId] = useState<number | undefined>(undefined)
    const [players, setPlayers] = useState<Record<number, Player>>({})
    const [playersPositions, setPlayersPositions] = useState<
        Record<"left" | "right", number | undefined>
    >({
        left: undefined,
        right: undefined,
    })
    const [winner, setWinner] = useState<number | undefined>(undefined)

    useEffect(() => {
        const canvas = canvasElement.current!
        const ctx = canvas.getContext("2d")!

        resizeCanvas(canvas, ctx)

        function r() {
            resizeCanvas(canvas, ctx)
        }

        window.addEventListener("resize", r)
        return () => {
            window.removeEventListener("resize", r)
        }
    }, [])

    useEffect(() => {
        connection.current = connectToGameServer(singleplayer)

        function onMessageLobbyState(myConnId: number, otherConnId: number | undefined) {
            setConnId(myConnId)

            const players: Record<number, Player> = {}
            players[myConnId] = {
                connId: myConnId,
                me: true,
                score: 0,
                ready: false,
            }

            if (otherConnId !== undefined) {
                players[otherConnId] = {
                    connId: otherConnId,
                    me: false,
                    score: 0,
                    ready: false,
                }
                if (singleplayer) {
                    setPlayersPositions({
                        left: myConnId,
                        right: otherConnId,
                    })
                } else {
                    setPlayersPositions({
                        left: otherConnId,
                        right: myConnId,
                    })
                }
                setStatus("game-start")
            } else {
                setPlayersPositions({ left: myConnId, right: undefined })
                setStatus("waiting")
            }

            setPlayers(players)
        }

        function onMessageJoined(connId: number) {
            setPlayers((prev) => ({
                ...prev,
                [connId]: {
                    connId,
                    me: false,
                    score: 0,
                    ready: false,
                },
            }))
            setPlayersPositions((prev) => {
                prev.right = connId
                return { ...prev }
            })
            setStatus("game-start")
        }

        function onMessageStarted() {
            setStatus("playing")
            setPlayers((prev) => {
                for (const connId of Object.keys(prev)) {
                    prev[Number(connId)].ready = false
                }
                return { ...prev }
            })
        }

        function onMessageReady(connId: number) {
            setPlayers((prev) => {
                prev[connId].ready = true
                return { ...prev }
            })
        }

        function onMessageGameEnd(winner: number) {
            setStatus("game-end")
            setWinner(winner)
            setPlayers((prev) => {
                prev[winner].score += 1
                return { ...prev }
            })
        }

        connection.current.onMessageLobbyState = onMessageLobbyState
        connection.current.onMessageJoined = onMessageJoined
        connection.current.onMessageStarted = onMessageStarted
        connection.current.onMessageReady = onMessageReady
        connection.current.onMessageGameEnd = onMessageGameEnd

        return connection.current.close
    }, [])

    useEffect(() => {
        const canvas = canvasElement.current!
        const ctx = canvas.getContext("2d")!

        connection.current.onMessageGameState = function (
            paddles: Paddle[],
            ballX: number,
            ballY: number,
        ) {
            const paddlesDraw: PaddleDraw[] = []
            for (const paddle of paddles) {
                paddlesDraw.push({
                    y: paddle.y,
                    left: playersPositions.left === paddle.connId,
                    me: paddle.connId === connId,
                })
            }
            drawGameState(ctx, paddlesDraw, ballX, ballY)
        }
    }, [connId, playersPositions])

    return (
        <div className="w-full max-w-[800px] mt-10">
            <div className="w-full max-w-[800px] flex items-center justify-between">
                <PlayerScore connId={playersPositions.left} players={players} />
                <PlayerScore connId={playersPositions.right} players={players} />
            </div>

            <div className="relative w-full max-w-[800px] mt-6 aspect-[2/1] mx-auto canvas-wrapper">
                <canvas ref={canvasElement} id="canvas" className="w-full aspect-[2/1]"></canvas>
                <div className="absolute inset-0 flex items-center justify-center">
                    <MenuScreen
                        status={status}
                        onStart={connection.current?.sendStartMessage}
                        winner={winner}
                        players={players}
                        onPlayAgain={connection.current?.sendStartMessage}
                    />
                </div>
            </div>

            <div className="flex gap-8 items-center w-fit mx-auto mt-6">
                <div className="flex flex-col items-center gap-2">
                    <div className="bg-dark w-fit p-2 rounded">
                        <svg className="size-4 text-light">
                            <use href="/icons.svg#arrow-up"></use>
                        </svg>
                    </div>
                    <p className="text-xs">up</p>
                </div>
                <div className="flex flex-col items-center gap-2">
                    <div className="bg-dark w-fit p-2 rounded">
                        <svg className="size-4 text-light rotate-180">
                            <use href="/icons.svg#arrow-up"></use>
                        </svg>
                    </div>
                    <p className="text-xs">down</p>
                </div>
            </div>
        </div>
    )
}
