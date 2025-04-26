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
        List<FileMetadata> metadata = await GetMetadataByIdOrThrow(request.ItemsList);
        List<Tag> tags = await GetTagsByNameOrThrow(request.Tags);
        foreach (FileMetadata item in metadata)
        {
            item.Tags.Clear();
            item.Tags.AddRange(tags);
        }

        await dbContext.SaveChangesAsync(cancellationToken);
        return metadata.Select(LibraryItemDto.FromMetadata).ToList();
    }

    private async Task<List<FileMetadata>> GetMetadataByIdOrThrow(List<uint> ids)
    {
        List<FileMetadata> metadata = await dbContext.FilesMetadata
            .Include(x => x.Tags)
            .Where(x => ids.Contains(x.Id))
            .ToListAsync();
        if (metadata.Count != ids.Count)
        {
            throw new ApplicationException("Some metadata not found");
        }

        return metadata;
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