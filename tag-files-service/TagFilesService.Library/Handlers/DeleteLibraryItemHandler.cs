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
        // TODO: If item not found -> try to find the object with item.id tag in library bucket
        LibraryItem libraryItem = await dbContext.LibraryItems
            .FirstAsync(x => x.Id == request.Id, cancellationToken);

        if (libraryItem.ThumbnailStatus is ThumbnailStatus.Generated)
        {
            string thumbnailFileName = LibraryItem.ChangeFileExtension(libraryItem.FileName, ".jpg");
            await TryRemoveObject(Buckets.Thumbnail, thumbnailFileName, cancellationToken);
        }

        await TryRemoveObject(Buckets.Library, libraryItem.FileName, cancellationToken);

        dbContext.Remove(libraryItem);
        await dbContext.SaveChangesAsync(cancellationToken);
        logger.LogInformation("Deleted file id '{Id}' from library", libraryItem.Id);
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