export function initCanvas(canvas: HTMLCanvasElement, ctx: CanvasRenderingContext2D) {
    function resizeCanvas() {
        ctx.setTransform(1, 0, 0, 1, 0, 0)

        const r = canvas.getBoundingClientRect()
        canvas.width = r.width
        canvas.height = r.height

        const scaleX = canvas.width / 800
        const scaleY = canvas.height / 400
        ctx.scale(scaleX, scaleY)
    }

    window.addEventListener("DOMContentLoaded", resizeCanvas)
    window.addEventListener("resize", resizeCanvas)
}

export const gameWidth = 800
export const gameHeight = 400

export const PADDLE_WIDTH = 10
export const PADDLE_HEIGHT = 100
export const BALL_RADIUS = 5

const paddleXPosition: Record<number, number> = {
    0: 50,
    1: gameWidth - 50 - PADDLE_WIDTH,
}

export type Paddle = {
    connId: number
    y: number
}

export function drawGameState(
    ctx: CanvasRenderingContext2D,
    connId: number,
    connOrder: number,
    paddles: Paddle[],
    ballX: number,
    ballY: number,
) {
    ctx.clearRect(0, 0, gameWidth, gameHeight)

    for (const paddle of paddles) {
        let paddleX: number

        if (paddle.connId === connId) {
            ctx.fillStyle = "#eaeaea"
            paddleX = paddleXPosition[connOrder]
        } else {
            ctx.fillStyle = "#dd6e42"
            paddleX = paddleXPosition[(connOrder + 1) % 2]
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
