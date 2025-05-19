using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;

namespace TagFilesService.FilesProcessing;

public class VideoConversionService(
    ILogger<VideoConversionService> logger,
    VideoConversionQueue queue) : BackgroundService
{
    public override async Task StopAsync(CancellationToken stoppingToken)
    {
        logger.LogInformation("VideoFileConverterService is stopping.");
        await base.StopAsync(stoppingToken);
    }

    protected override Task ExecuteAsync(CancellationToken stoppingToken)
    {
        logger.LogInformation("VideoFileConverterService is starting.");
        return ProcessTaskQueue(stoppingToken);
    }

    private async Task ProcessTaskQueue(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                string fileName = await queue.Dequeue(stoppingToken);

                logger.LogInformation("Processing video file: {VideoFile}", fileName);
                await Task.Delay(5000, stoppingToken);
                logger.LogInformation("Finished processing video file: {VideoFile}", fileName);
            }
            catch (OperationCanceledException)
            {
            }
            catch (Exception ex)
            {
                logger.LogError(ex, "Error occurred executing task.");
            }
        }
    }
}