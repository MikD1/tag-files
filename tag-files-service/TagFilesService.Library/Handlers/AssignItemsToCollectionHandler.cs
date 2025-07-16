using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers;

public class AssignItemsToCollectionHandler(AppDbContext dbContext)
    : IRequestHandler<AssignItemsToCollectionRequest, List<LibraryItemDto>>
{
    public async Task<List<LibraryItemDto>> Handle(AssignItemsToCollectionRequest request,
        CancellationToken cancellationToken)
    {
        List<LibraryItem> libraryItems = await GetLibraryItemsByIdOrThrow(request.ItemsList);
        await EnsureCollectionExists(request.CollectionId);
        foreach (LibraryItem item in libraryItems)
        {
            item.CollectionId = request.CollectionId;
        }

        await dbContext.SaveChangesAsync(cancellationToken);
        return libraryItems.Select(LibraryItemDto.FromModel).ToList();
    }

    private async Task<List<LibraryItem>> GetLibraryItemsByIdOrThrow(List<uint> ids)
    {
        List<LibraryItem> libraryItems = await dbContext.LibraryItems
            .Include(x => x.Tags)
            .Where(x => ids.Contains(x.Id))
            .ToListAsync();
        if (libraryItems.Count != ids.Count)
        {
            throw new ApplicationException("Some items not found");
        }

        return libraryItems;
    }

    private async Task EnsureCollectionExists(uint collectionId)
    {
        bool exists = await dbContext.Collections.AnyAsync(x => x.Id == collectionId);
        if (!exists)
        {
            throw new ApplicationException($"Collection '{collectionId}' not found");
        }
    }
}