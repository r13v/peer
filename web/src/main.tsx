import "@fontsource/inter/400.css"
import "@fontsource/inter/600.css"
import "@xyflow/react/dist/style.css"
import "./styles.css"
import { createRoot } from "react-dom/client"
import { BoardPage } from "./components/board-page.tsx"
import { HomePage } from "./components/home-page.tsx"

const root = document.getElementById("root")
if (!root) {
	throw new Error("missing #root")
}
// "/b/{key}" is a board; every other path is the home page.
const boardKey = /^\/b\/([^/]+)\/?$/.exec(window.location.pathname)?.[1]

createRoot(root).render(
	boardKey ? <BoardPage boardKey={boardKey} /> : <HomePage />,
)
