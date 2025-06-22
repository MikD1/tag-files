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
        LibraryItem? libraryItem = await dbContext.LibraryItems
            .FirstOrDefaultAsync(x => x.Id == request.Id, cancellationToken);
        if (libraryItem is null)
        {
            throw new ApplicationException($"Item '{request.Id}' not found");
        }

        libraryItem.ToggleFavorite();
        await dbContext.SaveChangesAsync(cancellationToken);
    }
}