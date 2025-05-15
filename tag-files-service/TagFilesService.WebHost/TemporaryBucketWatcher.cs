using System.Text.Json;
using Minio;
using Minio.DataModel.Notification;
using TagFilesService.Library;
using TagFilesService.Model;

namespace TagFilesService.WebHost;

public class TemporaryBucketWatcher(
    ILogger<TemporaryBucketWatcher> logger,
    IMinioClient minioClient,
    IServiceScopeFactory serviceScopeFactory) : IHostedService
{
    public Task StartAsync(CancellationToken cancellationToken)
    {
        IObservable<MinioNotificationRaw> observable =
            minioClient.ListenBucketNotificationsAsync(Buckets.Temporary, [EventType.ObjectCreatedAll],
                cancellationToken: cancellationToken);
        _subscription = observable.Subscribe(OnNext);
        logger.LogInformation("Temporary bucket watcher service started");
        return Task.CompletedTask;
    }

    public Task StopAsync(CancellationToken cancellationToken)
    {
        _subscription?.Dispose();
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

        using IServiceScope scope = serviceScopeFactory.CreateScope();
        FilesProcessing processing = scope.ServiceProvider.GetRequiredService<FilesProcessing>();
        await processing.ProcessFile(info.FileName, info.MediaType);
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