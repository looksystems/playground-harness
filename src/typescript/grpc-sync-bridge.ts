/**
 * gRPC sync bridge for OpenShellGrpcDriver.
 *
 * This script is invoked as a subprocess by the driver to perform async gRPC
 * calls synchronously. It receives a command, endpoint, and base64-encoded
 * request, makes the gRPC call, and prints the result as JSON to stdout.
 *
 * Usage:
 *   node grpc-sync-bridge.js <command> <endpoint> <base64-request>
 *
 * Commands:
 *   create-sandbox  — calls CreateSandbox RPC
 *   delete-sandbox  — calls DeleteSandbox RPC
 *   exec-sandbox    — calls ExecSandbox (server-streaming), collects all
 *                     chunks, prints { stdout, stderr, exitCode }
 */

import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import * as path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const protoPath = path.resolve(__dirname, "../../proto/openshell/openshell.proto");

function loadClient(endpoint: string): any {
  const packageDef = protoLoader.loadSync(protoPath, {
    keepCase: true,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true,
    includeDirs: [path.resolve(__dirname, "../../proto")],
  });
  const proto = grpc.loadPackageDefinition(packageDef) as any;
  return new proto.openshell.OpenShell(
    endpoint,
    grpc.credentials.createInsecure(),
  );
}

async function createSandbox(endpoint: string, requestJson: string): Promise<void> {
  const client = loadClient(endpoint);
  const request = JSON.parse(requestJson);
  return new Promise((resolve, reject) => {
    client.CreateSandbox(request, (err: Error | null, response: any) => {
      if (err) {
        reject(err);
        return;
      }
      process.stdout.write(JSON.stringify(response));
      resolve();
    });
  });
}

async function deleteSandbox(endpoint: string, requestJson: string): Promise<void> {
  const client = loadClient(endpoint);
  const request = JSON.parse(requestJson);
  return new Promise((resolve, reject) => {
    client.DeleteSandbox(request, (err: Error | null, response: any) => {
      if (err) {
        reject(err);
        return;
      }
      process.stdout.write(JSON.stringify(response ?? {}));
      resolve();
    });
  });
}

async function execSandbox(endpoint: string, requestJson: string): Promise<void> {
  const client = loadClient(endpoint);
  const request = JSON.parse(requestJson);
  const stream = client.ExecSandbox(request);

  const stdoutParts: string[] = [];
  const stderrParts: string[] = [];
  let exitCode = 0;

  return new Promise((resolve, reject) => {
    stream.on("data", (event: any) => {
      if (event.stdout?.data) {
        stdoutParts.push(Buffer.from(event.stdout.data).toString("utf-8"));
      } else if (event.stderr?.data) {
        stderrParts.push(Buffer.from(event.stderr.data).toString("utf-8"));
      } else if (event.exit) {
        exitCode = event.exit.code ?? 0;
      }
    });
    stream.on("end", () => {
      process.stdout.write(JSON.stringify({
        stdout: stdoutParts.join(""),
        stderr: stderrParts.join(""),
        exitCode,
      }));
      resolve();
    });
    stream.on("error", (err: Error) => {
      reject(err);
    });
  });
}

async function main(): Promise<void> {
  const [command, endpoint, base64Request] = process.argv.slice(2);
  if (!command || !endpoint || !base64Request) {
    console.error("Usage: grpc-sync-bridge <command> <endpoint> <base64-request>");
    process.exit(1);
  }
  const requestJson = Buffer.from(base64Request, "base64").toString("utf-8");

  switch (command) {
    case "create-sandbox":
      await createSandbox(endpoint, requestJson);
      break;
    case "delete-sandbox":
      await deleteSandbox(endpoint, requestJson);
      break;
    case "exec-sandbox":
      await execSandbox(endpoint, requestJson);
      break;
    default:
      console.error(`Unknown command: ${command}`);
      process.exit(1);
  }
}

main().catch((err) => {
  console.error(err.message);
  process.exit(1);
});
