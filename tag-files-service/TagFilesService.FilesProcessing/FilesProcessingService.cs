using System.Diagnostics;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Minio;
using Minio.DataModel.Args;
using Minio.DataModel.Tags;
using TagFilesService.Infrastructure;
using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.FilesProcessing;

public class FilesProcessingService(
    ILogger<FilesProcessingService> logger,
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
                logger.LogInformation("Start processing file");
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
            if (processingFile.FileType is FileType.Video)
            {
                await ConvertVideoFileAndAddToLibrary(dbContext, processingFile, cancellationToken);
            }
            else
            {
                await AddFileToLibrary(dbContext, processingFile, cancellationToken);
            }

            await DeleteTemporaryFile(dbContext, processingFile, cancellationToken);
            FileMetadata metadata = await SaveMetadata(dbContext, processingFile, cancellationToken);
            if (metadata.Type is FileType.Video)
            {
                await FillVideoDuration(dbContext, metadata, cancellationToken);
            }

            if (metadata.Type is FileType.Image or FileType.Video)
            {
                await MakeThumbnail(dbContext, metadata, cancellationToken);
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
        logger.LogInformation("File added to library ({id})", processingFile.Id);
    }

    private async Task ConvertVideoFileAndAddToLibrary(AppDbContext dbContext, ProcessingFile processingFile,
        CancellationToken cancellationToken)
    {
        processingFile.Status = ProcessingStatus.Converting;
        await dbContext.SaveChangesAsync(cancellationToken);
        logger.LogInformation("Start video file converting ({id})", processingFile.Id);

        string sourceUrl = Path.Combine(minio.Config.Endpoint, Buckets.Temporary, processingFile.OriginalFileName);
        ProcessStartInfo startInfo = new()
        {
            FileName = "ffmpeg",
            Arguments = $"-i \"{sourceUrl}\" -c:v libx264 -c:a aac -f mp4 -movflags frag_keyframe+empty_moov pipe:1",
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true
        };

        using Process ffmpeg = new();
        ffmpeg.StartInfo = startInfo;
        ffmpeg.Start();
        Task loggingTask = LogFfmpegOutput(ffmpeg, cancellationToken);

        await minio.PutObjectAsync(new PutObjectArgs()
            .WithBucket(Buckets.Library)
            .WithObject(processingFile.LibraryFileName)
            .WithStreamData(ffmpeg.StandardOutput.BaseStream)
            .WithObjectSize(-1)
            .WithContentType("video/mp4"), cancellationToken);

        await ffmpeg.WaitForExitAsync(cancellationToken);
        await loggingTask;
        if (ffmpeg.ExitCode != 0)
        {
            throw new ApplicationException("ffmpeg exited with error code " + ffmpeg.ExitCode);
        }

        processingFile.Status = ProcessingStatus.AddedToLibrary;
        await dbContext.SaveChangesAsync(cancellationToken);
        logger.LogInformation("Finished video file converting ({id})", processingFile.Id);
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
        logger.LogInformation("Temporary file deleted ({id})", processingFile.Id);
    }

    private async Task<FileMetadata> SaveMetadata(AppDbContext dbContext, ProcessingFile processingFile,
        CancellationToken cancellationToken)
    {
        FileMetadata metadata = new(processingFile.LibraryFileName, processingFile.ContentType, null);
        await dbContext.FilesMetadata.AddAsync(metadata, cancellationToken);
        await dbContext.SaveChangesAsync(cancellationToken);

        Dictionary<string, string> tags = new() { ["metadata-id"] = metadata.Id.ToString() };
        SetObjectTagsArgs args = new SetObjectTagsArgs()
            .WithBucket(Buckets.Library)
            .WithObject(metadata.FileName)
            .WithTagging(Tagging.GetObjectTags(tags));
        await minio.SetObjectTagsAsync(args, cancellationToken);

        processingFile.Status = ProcessingStatus.MetadataSaved;
        await dbContext.SaveChangesAsync(cancellationToken);
        logger.LogInformation("Metadata saved ({id})", processingFile.Id);
        return metadata;
    }

    private async Task FillVideoDuration(AppDbContext dbContext, FileMetadata metadata,
        CancellationToken cancellationToken)
    {
        try
        {
            string fileUrl = Path.Combine(minio.Config.Endpoint, Buckets.Library, metadata.FileName);

            using Process ffmpeg = new();
            ffmpeg.StartInfo = new()
            {
                FileName = "ffmpeg",
                Arguments = $"""-i "{fileUrl}" -f null -""",
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
                CreateNoWindow = true
            };

            ffmpeg.Start();

            string output = await ffmpeg.StandardError.ReadToEndAsync(cancellationToken);
            await ffmpeg.WaitForExitAsync(cancellationToken);
            if (ffmpeg.ExitCode != 0)
            {
                string error = await ffmpeg.StandardError.ReadToEndAsync(cancellationToken);
                throw new ApplicationException(error);
            }

            bool success = VideoDurationParser.TryParse(output, out TimeSpan duration);
            if (!success)
            {
                throw new ApplicationException("Failed to parse video duration");
            }

            metadata.VideoDuration = duration;
            await dbContext.SaveChangesAsync(cancellationToken);
            logger.LogInformation("Video duration filled ({fileName})", metadata.FileName);
        }
        catch (Exception ex)
        {
            logger.LogError("Failed to get video duration for file {fileName}: {error}", metadata.FileName, ex.Message);
        }
    }

    private async Task MakeThumbnail(AppDbContext dbContext, FileMetadata metadata, CancellationToken cancellationToken)
    {
        try
        {
            string fileUrl = Path.Combine(minio.Config.Endpoint, Buckets.Library, metadata.FileName);

            string timestampArg = string.Empty;
            if (metadata.VideoDuration.HasValue)
            {
                TimeSpan thumbnailTimestamp = TimeSpan.FromTicks((long)(metadata.VideoDuration.Value.Ticks * 0.1));
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
                .WithObject(ChangeFileExtension(metadata.FileName, ".jpg"))
                .WithStreamData(thumbnailStream)
                .WithObjectSize(thumbnailStream.Length)
                .WithContentType("image/jpeg");
            await minio.PutObjectAsync(args, cancellationToken);

            metadata.UpdateThumbnailStatus(ThumbnailStatus.Generated);
            await dbContext.SaveChangesAsync(cancellationToken);
            logger.LogInformation("Thumbnail generated for file {fileName}", metadata.FileName);
        }
        catch (Exception ex)
        {
            metadata.UpdateThumbnailStatus(ThumbnailStatus.Failed);
            await dbContext.SaveChangesAsync(cancellationToken);
            logger.LogError("Failed to generate thumbnail for file {fileName}: {error}", metadata.FileName, ex.Message);
        }
    }

    private string ChangeFileExtension(string fileName, string newExtension)
    {
        string fileNameWithoutExtension = Path.GetFileNameWithoutExtension(fileName);
        return fileNameWithoutExtension + newExtension;
    }

    private async Task LogFfmpegOutput(Process ffmpeg, CancellationToken cancellationToken)
    {
        while (await ffmpeg.StandardError.ReadLineAsync(cancellationToken) is { } line)
        {
            logger.LogInformation(line);
        }
    }
}