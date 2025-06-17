using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers;

public class ToggleFavoriteRequestHandler(AppDbContext dbContext) : IRequestHandler<ToggleFavoriteRequest>
{
    public async Task Handle(ToggleFavoriteRequest request, CancellationToken cancellationToken)
    {
        FileMetadata? metadata = await dbContext.FilesMetadata
            .FirstOrDefaultAsync(x => x.Id == request.Id, cancellationToken);
        if (metadata is null)
        {
            throw new ApplicationException($"Metadata '{request.Id}' not found");
        }

        metadata.ToggleFavorite();
        await dbContext.SaveChangesAsync(cancellationToken);
    }
}