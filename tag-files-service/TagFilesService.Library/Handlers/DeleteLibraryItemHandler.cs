using MediatR;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Logging;
using Minio;
using Minio.DataModel.Args;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers;

public class DeleteLibraryItemHandler(
    ILogger<DeleteLibraryItemHandler> logger,
    IMinioClient minio,
    AppDbContext dbContext)
    : IRequestHandler<DeleteLibraryItemRequest>
{
    public async Task Handle(DeleteLibraryItemRequest request, CancellationToken cancellationToken)
    {
        // TODO: If metadata not found -> try to find the object with metadata.id tag in library bucket
        FileMetadata metadata = await dbContext.FilesMetadata
            .FirstAsync(x => x.Id == request.Id, cancellationToken);

        if (metadata.ThumbnailStatus is ThumbnailStatus.Generated)
        {
            string thumbnailFileName = FileMetadata.ChangeFileExtension(metadata.FileName, ".jpg");
            await TryRemoveObject(Buckets.Thumbnail, thumbnailFileName, cancellationToken);
        }

        await TryRemoveObject(Buckets.Library, metadata.FileName, cancellationToken);

        dbContext.Remove(metadata);
        await dbContext.SaveChangesAsync(cancellationToken);
        logger.LogInformation("Deleted file id '{Id}' from library", metadata.Id);
    }

    private async Task TryRemoveObject(string bucket, string objectName, CancellationToken cancellationToken)
    {
        try
        {
            await minio.RemoveObjectAsync(new RemoveObjectArgs()
                .WithBucket(bucket)
                .WithObject(objectName), cancellationToken);
            logger.LogInformation("Successfully removed object '{ObjectName}' from bucket '{Bucket}'", objectName,
                bucket);
        }
        catch (Exception ex)
        {
            logger.LogError(ex, "Failed to remove object '{ObjectName}' from bucket '{Bucket}'", objectName, bucket);
        }
    }
}