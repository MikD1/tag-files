using System.Text.Json;
using MediatR;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Minio;
using Minio.DataModel.Notification;
using TagFilesService.FilesProcessing.Contracts;
using TagFilesService.Model;

namespace TagFilesService.FilesProcessing;

public class BucketWatcher(
    ILogger<BucketWatcher> logger,
    IMinioClient minioClient,
    IServiceScopeFactory serviceScopeFactory) : IHostedService
{
    public Task StartAsync(CancellationToken cancellationToken)
    {
        IObservable<MinioNotificationRaw> observable =
            minioClient.ListenBucketNotificationsAsync(Buckets.Temporary, [EventType.ObjectCreatedAll],
                cancellationToken: cancellationToken);
        _subscription = observable.Subscribe(OnNext);
        logger.LogInformation("BucketWatcher service started");
        return Task.CompletedTask;
    }

    public Task StopAsync(CancellationToken cancellationToken)
    {
        _subscription?.Dispose();
        logger.LogInformation("BucketWatcher service stopped");
        return Task.CompletedTask;
    }

    private async void OnNext(MinioNotificationRaw notification)
    {
        (string? FileName, string? MediaType) info = GetFileInfo(notification.Json);
        if (info.FileName is null || info.MediaType is null)
        {
            logger.LogWarning("Failed to parse file info from notification");
            return;
        }

        FileProcessingRequest request = new(info.FileName, info.MediaType);

        using IServiceScope scope = serviceScopeFactory.CreateScope();
        IMediator mediator = scope.ServiceProvider.GetRequiredService<IMediator>();
        await mediator.Send(request);
    }

    private (string? FileName, string? MediaType) GetFileInfo(string json)
    {
        using JsonDocument document = JsonDocument.Parse(json);
        JsonElement objectElement = document.RootElement
            .GetProperty("Records")[0]
            .GetProperty("s3")
            .GetProperty("object");
        string? key = objectElement.GetProperty("key").GetString();
        string? contentType = objectElement.GetProperty("contentType").GetString();
        return (key, contentType);
    }

    private IDisposable? _subscription;
}