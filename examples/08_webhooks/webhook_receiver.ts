import { createServer, IncomingMessage, ServerResponse } from "node:http";

const port = Number(process.env.PORT ?? "8787");

function readBody(req: IncomingMessage): Promise<string> {
	return new Promise((resolve, reject) => {
		const chunks: Buffer[] = [];
		req.on("data", (chunk) => {
			chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
		});
		req.on("end", () => {
			resolve(Buffer.concat(chunks).toString("utf8"));
		});
		req.on("error", reject);
	});
}

function writeJSON(res: ServerResponse, status: number, payload: unknown): void {
	res.statusCode = status;
	res.setHeader("Content-Type", "application/json");
	res.end(JSON.stringify(payload));
}

const server = createServer(async (req, res) => {
	if (req.method !== "POST" || req.url !== "/webhook") {
		writeJSON(res, 404, {
			ok: false,
			error: "not found",
			route: `${req.method ?? ""} ${req.url ?? ""}`.trim(),
		});
		return;
	}

	const rawBody = await readBody(req);
	let parsedBody: unknown = rawBody;
	try {
		parsedBody = JSON.parse(rawBody);
	} catch {
		// Keep raw string when payload is not valid JSON.
	}

	console.log("---- incoming webhook ----");
	console.log("time:", new Date().toISOString());
	console.log("method:", req.method);
	console.log("url:", req.url);
	console.log("headers:", req.headers);
	console.log("body:", parsedBody);

	writeJSON(res, 200, {
		ok: true,
		received_at: new Date().toISOString(),
	});
});

server.listen(port, () => {
	console.log(`Webhook receiver listening on http://localhost:${port}/webhook`);
});
