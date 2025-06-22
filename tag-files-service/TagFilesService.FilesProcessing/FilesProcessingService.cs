using System.Diagnostics;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Minio;
using Minio.DataModel.Args;
using Minio.DataModel.Tags;
using TagFilesService.Infrastructure;
using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.FilesProcessing;

public class FilesProcessingService(
    ILogger<FilesProcessingService> logger,
    IOptions<FfmpegOptions> ffmpegOptions,
    FilesProcessingQueue queue,
    IMinioClient minio,
    IServiceScopeFactory serviceScopeFactory) : BackgroundService
{
    public override async Task StopAsync(CancellationToken stoppingToken)
    {
        logger.LogInformation("FilesProcessing service stopping");
        await base.StopAsync(stoppingToken);
    }

    protected override Task ExecuteAsync(CancellationToken stoppingToken)
    {
        logger.LogInformation("FilesProcessing service starting");
        return ProcessQueue(stoppingToken);
    }

    private async Task ProcessQueue(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                ProcessingFile processingFile = await queue.Dequeue(stoppingToken);
                logger.LogInformation("Start processing file ({ProcessingFileFileType})", processingFile.FileType);
                await ProcessFile(processingFile, stoppingToken);
                logger.LogInformation("Finished processing file");
            }
            catch (OperationCanceledException)
            {
            }
            catch (Exception ex)
            {
                logger.LogError(ex, "Error while processing file");
            }
        }
    }

    private async Task ProcessFile(ProcessingFile processingFile, CancellationToken cancellationToken)
    {
        using IServiceScope scope = serviceScopeFactory.CreateScope();
        AppDbContext dbContext = scope.ServiceProvider.GetRequiredService<AppDbContext>();
        dbContext.Update(processingFile);

        try
        {
            TimeSpan? duration = null;
            if (processingFile.FileType is FileType.Video)
            {
                duration = await ConvertVideoFileAndAddToLibrary(dbContext, processingFile, cancellationToken);
            }
            else
            {
                await AddFileToLibrary(dbContext, processingFile, cancellationToken);
            }

            await DeleteTemporaryFile(dbContext, processingFile, cancellationToken);
            LibraryItem libraryItem = await SaveLibraryItem(dbContext, processingFile, cancellationToken);
            if (duration is not null)
            {
                libraryItem.VideoDuration = duration.Value;
                await dbContext.SaveChangesAsync(cancellationToken);
            }

            if (libraryItem.FileType is FileType.Image or FileType.Video)
            {
                await MakeThumbnail(dbContext, libraryItem, cancellationToken);
            }

            processingFile.Status = ProcessingStatus.Done;
            await dbContext.SaveChangesAsync(cancellationToken);
        }
        catch (Exception)
        {
            processingFile.Status = ProcessingStatus.Failed;
            await dbContext.SaveChangesAsync(cancellationToken);
            throw;
        }
    }

    private async Task AddFileToLibrary(AppDbContext dbContext, ProcessingFile processingFile,
        CancellationToken cancellationToken)
    {
        CopySourceObjectArgs sourceArgs = new CopySourceObjectArgs()
            .WithBucket(Buckets.Temporary)
            .WithObject(processingFile.OriginalFileName);
        CopyObjectArgs args = new CopyObjectArgs()
            .WithBucket(Buckets.Library)
            .WithObject(processingFile.LibraryFileName)
            .WithCopyObjectSource(sourceArgs);
        await minio.CopyObjectAsync(args, cancellationToken);

        processingFile.Status = ProcessingStatus.AddedToLibrary;
        await dbContext.SaveChangesAsync(cancellationToken);
        logger.LogInformation("File added to library - {id}", processingFile.Id);
    }

    private async Task<TimeSpan> ConvertVideoFileAndAddToLibrary(AppDbContext dbContext, ProcessingFile processingFile,
        CancellationToken cancellationToken)
    {
        processingFile.Status = ProcessingStatus.Converting;
        await dbContext.SaveChangesAsync(cancellationToken);

        logger.LogInformation("Start video file converting (preset: {preset}, crf: {crf}) - {id}",
            ffmpegOptions.Value.Preset, ffmpegOptions.Value.Crf, processingFile.Id);

        string sourceUrl = Path.Combine(minio.Config.Endpoint, Buckets.Temporary, processingFile.OriginalFileName);
        string tempFilePath = Path.Combine(Path.GetTempPath(), Guid.NewGuid() + ".tagfilestmp");
        TimeSpan totalDuration = TimeSpan.Zero;
        try
        {
            ProcessStartInfo startInfo = new()
            {
                FileName = "ffmpeg",
                Arguments =
                    $"-i \"{sourceUrl}\" -c:v libx264 -preset {ffmpegOptions.Value.Preset} -crf {ffmpegOptions.Value.Crf} -c:a aac -b:a 128k -f mp4 -movflags +faststart \"{tempFilePath}\"",
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
                CreateNoWindow = true
            };

            using Process ffmpeg = new();
            ffmpeg.StartInfo = startInfo;
            ffmpeg.Start();
            Task<VideoConversionProgress> loggingTask = LogFfmpegOutput(ffmpeg, cancellationToken);
            await ffmpeg.WaitForExitAsync(cancellationToken);
            VideoConversionProgress progress = await loggingTask;
            if (ffmpeg.ExitCode != 0)
            {
                throw new ApplicationException("ffmpeg exited with error\n" + progress.RawOutput);
            }

            await using FileStream fileStream = File.OpenRead(tempFilePath);
            FileInfo fileInfo = new(tempFilePath);

            await minio.PutObjectAsync(new PutObjectArgs()
                .WithBucket(Buckets.Library)
                .WithObject(processingFile.LibraryFileName)
                .WithStreamData(fileStream)
                .WithObjectSize(fileInfo.Length)
                .WithContentType("video/mp4"), cancellationToken);

            if (progress.Total is not null)
            {
                totalDuration = progress.Total.Value;
            }
        }
        finally
        {
            File.Delete(tempFilePath);
        }

        processingFile.Status = ProcessingStatus.AddedToLibrary;
        await dbContext.SaveChangesAsync(cancellationToken);
        logger.LogInformation("Finished video file converting - {id}", processingFile.Id);
        return totalDuration;
    }

    private async Task DeleteTemporaryFile(AppDbContext dbContext, ProcessingFile processingFile,
        CancellationToken cancellationToken)
    {
        RemoveObjectArgs args = new RemoveObjectArgs()
            .WithBucket(Buckets.Temporary)
            .WithObject(processingFile.OriginalFileName);
        await minio.RemoveObjectAsync(args, cancellationToken);

        processingFile.Status = ProcessingStatus.TemporaryFileDeleted;
        await dbContext.SaveChangesAsync(cancellationToken);
        logger.LogInformation("Temporary file deleted - {id}", processingFile.Id);
    }

    private async Task<LibraryItem> SaveLibraryItem(AppDbContext dbContext, ProcessingFile processingFile,
        CancellationToken cancellationToken)
    {
        LibraryItem libraryItem = new(processingFile.LibraryFileName, processingFile.FileType, null);
        await dbContext.LibraryItems.AddAsync(libraryItem, cancellationToken);
        await dbContext.SaveChangesAsync(cancellationToken);

        Dictionary<string, string> tags = new() { ["item-id"] = libraryItem.Id.ToString() };
        SetObjectTagsArgs args = new SetObjectTagsArgs()
            .WithBucket(Buckets.Library)
            .WithObject(libraryItem.FileName)
            .WithTagging(Tagging.GetObjectTags(tags));
        await minio.SetObjectTagsAsync(args, cancellationToken);

        processingFile.Status = ProcessingStatus.LibraryItemSaved;
        await dbContext.SaveChangesAsync(cancellationToken);
        logger.LogInformation("Library item saved - {id}", processingFile.Id);
        return libraryItem;
    }

    private async Task MakeThumbnail(AppDbContext dbContext, LibraryItem libraryItem, CancellationToken cancellationToken)
    {
        try
        {
            string fileUrl = Path.Combine(minio.Config.Endpoint, Buckets.Library, libraryItem.FileName);

            string timestampArg = string.Empty;
            if (libraryItem.VideoDuration.HasValue)
            {
                TimeSpan thumbnailTimestamp = TimeSpan.FromTicks((long)(libraryItem.VideoDuration.Value.Ticks * 0.1));
                timestampArg = $"-ss {thumbnailTimestamp}";
            }

            using Process ffmpeg = new();
            ffmpeg.StartInfo = new()
            {
                FileName = "ffmpeg",
                Arguments =
                    $"""{timestampArg} -i "{fileUrl}" -vf scale=300:-1 -vframes 1 -f image2pipe -vcodec mjpeg -""",
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
                CreateNoWindow = true
            };

            ffmpeg.Start();

            using MemoryStream thumbnailStream = new();
            await ffmpeg.StandardOutput.BaseStream.CopyToAsync(thumbnailStream, cancellationToken);
            thumbnailStream.Seek(0, SeekOrigin.Begin);

            await ffmpeg.WaitForExitAsync(cancellationToken);
            if (ffmpeg.ExitCode != 0)
            {
                string error = await ffmpeg.StandardError.ReadToEndAsync(cancellationToken);
                throw new ApplicationException(error);
            }

            PutObjectArgs args = new PutObjectArgs()
                .WithBucket(Buckets.Thumbnail)
                .WithObject(LibraryItem.ChangeFileExtension(libraryItem.FileName, ".jpg"))
                .WithStreamData(thumbnailStream)
                .WithObjectSize(thumbnailStream.Length)
                .WithContentType("image/jpeg");
            await minio.PutObjectAsync(args, cancellationToken);

            libraryItem.UpdateThumbnailStatus(ThumbnailStatus.Generated);
            await dbContext.SaveChangesAsync(cancellationToken);
            logger.LogInformation("Thumbnail generated for file {fileName}", libraryItem.FileName);
        }
        catch (Exception ex)
        {
            libraryItem.UpdateThumbnailStatus(ThumbnailStatus.Failed);
            await dbContext.SaveChangesAsync(cancellationToken);
            logger.LogError("Failed to generate thumbnail for file {fileName}: {error}", libraryItem.FileName, ex.Message);
        }
    }

    private async Task<VideoConversionProgress> LogFfmpegOutput(Process ffmpeg, CancellationToken cancellationToken)
    {
        VideoConversionProgress progress = new();
        int previousPercent = -1;
        while (await ffmpeg.StandardError.ReadLineAsync(cancellationToken) is { } line)
        {
            progress.AddOutputLine(line);
            if (progress.Total is null || progress.Percent == previousPercent)
            {
                continue;
            }

            previousPercent = progress.Percent;
            logger.LogInformation("Progress: {current} / {total} ({percent}%)", progress.Current ?? TimeSpan.Zero,
                progress.Total,
                progress.Percent);
        }

        return progress;
    }
}