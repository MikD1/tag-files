using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers;

public class AssignTagsHandler(AppDbContext dbContext) : IRequestHandler<AssignTagsRequest, List<LibraryItemDto>>
{
    public async Task<List<LibraryItemDto>> Handle(AssignTagsRequest request, CancellationToken cancellationToken)
    {
        List<LibraryItem> libraryItems = await GetLibraryItemByIdOrThrow(request.ItemsList);
        List<Tag> tags = await GetTagsByNameOrThrow(request.Tags);
        foreach (LibraryItem item in libraryItems)
        {
            item.Tags.Clear();
            item.Tags.AddRange(tags);
        }

        await dbContext.SaveChangesAsync(cancellationToken);
        return libraryItems.Select(LibraryItemDto.FromModel).ToList();
    }

    private async Task<List<LibraryItem>> GetLibraryItemByIdOrThrow(List<uint> ids)
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

    private async Task<List<Tag>> GetTagsByNameOrThrow(List<string> tagNames)
    {
        List<Tag> tags = await dbContext.Tags
            .Where(x => tagNames.Contains(x.Name))
            .ToListAsync();
        if (tags.Count != tagNames.Count)
        {
            throw new ApplicationException("Some tags not found");
        }

        return tags;
    }
}