// 127.0.0.1 is never a valid passkey domain (the browser says "This is an invalid domain"), while
// http://localhost is: the same server on the same port, so move there before anything renders.
if (location.hostname === "127.0.0.1") location.replace(location.href.replace("//127.0.0.1", "//localhost"));
import { render } from "preact";
import { App } from "./app";
import "./style.css";
import { initTheme } from "./theme";

initTheme();

render(<App />, document.getElementById("app")!);
