<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

/**
 * Hand-written gRPC client stub for the OpenShell service.
 *
 * When the grpc PHP extension and protoc-gen-grpc are available,
 * run `make proto` to regenerate from vendored .proto files.
 */
class OpenShellClient
{
    private $client;

    public function __construct(string $hostname, array $opts = [])
    {
        if (!class_exists(\Grpc\BaseStub::class)) {
            throw new \RuntimeException(
                'grpc PHP extension required. Install via: pecl install grpc'
            );
        }
        $this->client = new class($hostname, $opts) extends \Grpc\BaseStub {};
    }

    public function CreateSandbox(CreateSandboxRequest $request, array $metadata = []): CreateSandboxResponse
    {
        $response = $this->client->_simpleRequest(
            '/openshell.OpenShell/CreateSandbox',
            $request,
            [CreateSandboxResponse::class, 'decode'],
            $metadata,
        );
        return $response->wait()[0];
    }

    public function DeleteSandbox(DeleteSandboxRequest $request, array $metadata = []): void
    {
        $response = $this->client->_simpleRequest(
            '/openshell.OpenShell/DeleteSandbox',
            $request,
            [DeleteSandboxRequest::class, 'decode'],
            $metadata,
        );
        $response->wait();
    }

    /**
     * @return \Generator<ExecSandboxEvent>
     */
    public function ExecSandbox(ExecSandboxRequest $request, array $metadata = []): \Generator
    {
        $call = $this->client->_serverStreamRequest(
            '/openshell.OpenShell/ExecSandbox',
            $request,
            [ExecSandboxEvent::class, 'decode'],
            $metadata,
        );
        foreach ($call->responses() as $response) {
            yield $response;
        }
    }
}
