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

type GameStatus =
    | "connecting"
    | "waiting"
    | "waiting-player-left"
    | "game-start"
    | "game-end"
    | "playing"

type MenuScreenProps = {
    status: GameStatus
    winner: number | undefined
    players: Record<number, Player>
    saved: boolean
    onStart: (() => void) | undefined
    onPlayAgain: (() => void) | undefined
}

function MenuScreen({ status, winner, players, saved, onStart, onPlayAgain }: MenuScreenProps) {
    switch (status) {
        case "connecting":
            return <p className="text-white">Connecting</p>
        case "waiting":
            return <p className="text-white">Waiting for other player</p>
        case "waiting-player-left":
            return (
                <div className="flex flex-col gap-4 items-center justify-center">
                    <p className="text-white">Other player left</p>
                    <p className="text-white">Waiting for other player</p>
                </div>
            )
        case "game-start":
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
    singleplayer: boolean
    left: boolean
}

function PlayerScore({ singleplayer, connId, players, left }: PlayerScoreProps) {
    let hideCn = connId === undefined ? "opacity-0" : ""
    let me = false
    let score = 0
    let ready = false

    if (connId !== undefined) {
        ;({ me, score, ready } = players[connId])
    }

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
    const [saved, setSaved] = useState<boolean>(false)

    useEffect(() => {
        setSaved(false)
    }, [status])

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

        connection.current.onMessageLobbyState = function (
            myConnId: number,
            otherConnId: number | undefined,
        ) {
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

        connection.current.onMessageJoined = function (connId: number) {
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

        connection.current.onMessageStarted = function () {
            setStatus("playing")
            setPlayers((prev) => {
                for (const connId of Object.keys(prev)) {
                    prev[Number(connId)].ready = false
                }
                return { ...prev }
            })
        }

        connection.current.onMessageReady = function (connId: number) {
            setPlayers((prev) => {
                prev[connId].ready = true
                return { ...prev }
            })
        }

        connection.current.onMessageGameEnd = function (winner: number) {
            setStatus("game-end")
            setWinner(winner)
            setPlayers((prev) => {
                prev[winner].score += 1
                return { ...prev }
            })
        }

        connection.current.onMessageSaved = function () {
            setSaved(true)
        }

        return connection.current.close
    }, [])

    useEffect(() => {
        connection.current.onMessagePlayerLeft = function () {
            let connId: number
            for (const _cid in players) {
                const cid = Number(_cid)
                const p = players[cid]
                if (!p.me) {
                    connId = cid
                    break
                }
            }

            setPlayers((prev) => {
                delete prev[connId]
                return { ...prev }
            })
            setPlayersPositions((prev) => {
                if (prev.left === connId) {
                    prev.left = undefined
                } else {
                    prev.right = undefined
                }
                return { ...prev }
            })
            setStatus("waiting-player-left")
        }
    }, [players, playersPositions])

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

    useEffect(() => {
        function s(event: KeyboardEvent) {
            if (event.key === "r") {
                connection.current.sendStartMessage()
            }
        }

        if (status === "game-start" || status === "game-end") {
            window.addEventListener("keydown", s)
        }

        return () => {
            window.removeEventListener("keydown", s)
        }
    }, [status])

    return (
        <div className="w-full max-w-[800px] mt-10">
            <div className="w-full max-w-[800px] flex items-center justify-between">
                <PlayerScore
                    connId={playersPositions.left}
                    players={players}
                    singleplayer={singleplayer}
                    left={true}
                />
                <PlayerScore
                    connId={playersPositions.right}
                    players={players}
                    singleplayer={singleplayer}
                    left={false}
                />
            </div>

            <div className="relative w-full max-w-[800px] mt-6 aspect-[2/1] mx-auto canvas-wrapper">
                <canvas
                    ref={canvasElement}
                    className="w-full aspect-[2/1] border-3 border-orange bg-dark-3 crt-scanlines"
                ></canvas>
                <div className="absolute inset-0 flex items-center justify-center">
                    <MenuScreen
                        status={status}
                        onStart={connection.current?.sendStartMessage}
                        winner={winner}
                        players={players}
                        onPlayAgain={connection.current?.sendStartMessage}
                        saved={saved}
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
    )
}
