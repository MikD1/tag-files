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
        return Task.CompletedTask;
    }

    public Task StopAsync(CancellationToken cancellationToken)
    {
        _subscription?.Dispose();
        return Task.CompletedTask;
    }

    private async void OnNext(MinioNotificationRaw notification)
    {
        string? fileName = GetFileName(notification.Json);
        if (fileName is null)
        {
            logger.LogWarning("Failed to parse file name from notification");
            return;
        }

        using IServiceScope scope = serviceScopeFactory.CreateScope();
        FilesProcessing processing = scope.ServiceProvider.GetRequiredService<FilesProcessing>();
        await processing.ProcessFile(fileName);
    }

    private string? GetFileName(string json)
    {
        using JsonDocument document = JsonDocument.Parse(json);
        return document.RootElement
            .GetProperty("Records")[0]
            .GetProperty("s3")
            .GetProperty("object")
            .GetProperty("key")
            .GetString();
    }

    private IDisposable? _subscription;
}